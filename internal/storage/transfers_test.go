package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/lvm"
)

func transferConfig() config.Config {
	cfg := testConfig()
	cfg.MaxInboundTransfers = 2
	cfg.TransferGrace = time.Minute
	return cfg
}

// incomingLV renders a scan row whose device path points at a real
// file, so writes land somewhere inspectable.
func incomingLV(path string) string {
	return fmt.Sprintf(`{"report": [{"lv": [
		{"lv_name": "vol-2", "lv_uuid": "uuid-i", "vg_name": "vg0",
		 "lv_size": "2097664", "lv_attr": "Vwi-a-tz--",
		 "lv_tags": "beanstore.state=incoming,beanstore.transfer=tr-1",
		 "pool_lv": "pool0", "origin": "", "lv_path": %q, "lv_dm_path": "",
		 "data_percent": "", "metadata_percent": "", "lv_active": "active",
		 "lv_layout": "thin,sparse"}
	]}], "log": []}`, path)
}

// preparedTransfer registers tr-1 over a real backing file of one full
// chunk, one zero chunk and 512 final bytes.
func preparedTransfer(t *testing.T, fake *fakeRunner) (*Transfers, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "incoming")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	size := uint64(2*TransferChunkBytes + 512)
	require.NoError(t, os.Truncate(path, int64(size)))

	fake.outputs = append(fake.outputs, "", incomingLV(path))
	transfers := NewTransfers(t.Context(), lvm.New(lvm.WithRunner(fake)), transferConfig())
	require.NoError(t, transfers.PrepareReceive(t.Context(), "tr-1", "vol-2", size))

	return transfers, path
}

func TestTransferLifecycle(t *testing.T) {
	fake := &fakeRunner{}
	transfers, path := preparedTransfer(t, fake)
	size := uint64(2*TransferChunkBytes + 512)

	created := fake.commands()[0]
	assert.Contains(t, created, "--addtag beanstore.state=incoming")
	assert.Contains(t, created, "--addtag beanstore.transfer=tr-1")

	offset, err := transfers.NextOffset("tr-1")
	require.NoError(t, err)
	assert.Zero(t, offset)

	require.NoError(t, transfers.Attach("tr-1"))
	assert.ErrorIs(t, transfers.Attach("tr-1"), ErrTransferBusy)

	first := make([]byte, TransferChunkBytes)
	first[0] = 7
	require.NoError(t, transfers.Write("tr-1", 0, first))

	assert.ErrorIs(t, transfers.Write("tr-1", 42, first), ErrBadFrame, "unaligned")
	assert.ErrorIs(t, transfers.Write("tr-1", 0, first), ErrBadFrame, "before the accepted end")
	assert.ErrorIs(t, transfers.Write("tr-1", 4*TransferChunkBytes, first), ErrBadFrame, "beyond the size")

	final := []byte{9}
	require.NoError(t, transfers.Write("tr-1", 2*TransferChunkBytes, append(make([]byte, 511), final...)))

	offset, err = transfers.NextOffset("tr-1")
	require.NoError(t, err)
	assert.Equal(t, size, offset)

	transfers.Detach("tr-1")

	expected := NewDigestBuilder()
	expected.AddChunk(first)
	expected.AddZeroChunk(TransferChunkBytes)
	expected.AddChunk(append(make([]byte, 511), final...))
	require.NoError(t, transfers.Commit(t.Context(), "tr-1", expected.Sum(size)))

	content, err := os.ReadFile(path) //nolint:gosec // test file
	require.NoError(t, err)
	assert.Equal(t, byte(7), content[0])
	assert.Equal(t, byte(9), content[size-1])
	assert.Equal(t, make([]byte, TransferChunkBytes), content[TransferChunkBytes:2*TransferChunkBytes])

	commands := fake.commands()
	readied := commands[len(commands)-1]
	assert.Contains(t, readied, "--addtag beanstore.state=ready")
	assert.Contains(t, readied, "--deltag beanstore.state=incoming")
	assert.Contains(t, readied, "--deltag beanstore.transfer=tr-1")

	assert.NoError(t, transfers.Commit(t.Context(), "tr-1", nil), "a repeated commit answers OK")
	assert.ErrorIs(t, transfers.PrepareReceive(t.Context(), "tr-1", "vol-3", size), ErrTransferUsed)
}

func TestCommitRefusesDigestMismatch(t *testing.T) {
	fake := &fakeRunner{}
	transfers, _ := preparedTransfer(t, fake)

	err := transfers.Commit(t.Context(), "tr-1", []byte("wrong"))

	assert.ErrorIs(t, err, ErrDigestMismatch)
	assert.Contains(t, fake.commands(), "lvremove -f vg0/vol-2", "the mismatch destroys the transfer")
	assert.ErrorIs(t, transfers.Commit(t.Context(), "tr-1", nil), ErrTransferUnknown,
		"a destroyed transfer never turns committable")
}

func TestPrepareReceiveEnforcesLimit(t *testing.T) {
	fake := &fakeRunner{outputs: []string{"", incomingLV("/dev/null"), "", incomingLV("/dev/null")}}
	transfers := NewTransfers(t.Context(), lvm.New(lvm.WithRunner(fake)), transferConfig())

	require.NoError(t, transfers.PrepareReceive(t.Context(), "tr-1", "vol-2", 512))
	require.NoError(t, transfers.PrepareReceive(t.Context(), "tr-2", "vol-3", 512))

	assert.ErrorIs(t, transfers.PrepareReceive(t.Context(), "tr-3", "vol-4", 512), ErrTransferLimit)
}

func TestGraceExpiryDestroysTheTransfer(t *testing.T) {
	fake := &fakeRunner{}
	path := filepath.Join(t.TempDir(), "incoming")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	fake.outputs = append(fake.outputs, "", incomingLV(path))

	cfg := transferConfig()
	cfg.TransferGrace = 20 * time.Millisecond
	transfers := NewTransfers(t.Context(), lvm.New(lvm.WithRunner(fake)), cfg)
	require.NoError(t, transfers.PrepareReceive(t.Context(), "tr-1", "vol-2", 512))

	assert.Eventually(t, func() bool {
		_, err := transfers.NextOffset("tr-1")
		return err != nil
	}, time.Second, 5*time.Millisecond)

	assert.Eventually(t, func() bool {
		for _, command := range fake.commands() {
			if command == "lvremove -f vg0/vol-2" {
				return true
			}
		}
		return false
	}, time.Second, 5*time.Millisecond)
}
