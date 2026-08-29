package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDevice writes a file with a non-zero first chunk, a zero second
// chunk, a non-zero third chunk and a non-zero partial fourth chunk.
func testDevice(t *testing.T) (string, []byte) {
	t.Helper()

	content := make([]byte, 3*TransferChunkBytes+512)
	content[0] = 1
	content[2*TransferChunkBytes] = 2
	content[3*TransferChunkBytes+511] = 3

	path := filepath.Join(t.TempDir(), "device")
	require.NoError(t, os.WriteFile(path, content, 0o600))

	return path, content
}

func TestReadDeviceSkipsZeroChunks(t *testing.T) {
	path, content := testDevice(t)
	size := uint64(len(content))

	var frames []Frame
	gotSize, digest, err := ReadDevice(t.Context(), path, func(frame Frame) error {
		frames = append(frames, Frame{Offset: frame.Offset, Data: append([]byte(nil), frame.Data...)})
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, size, gotSize)
	assert.NotEmpty(t, digest)

	require.Len(t, frames, 3, "the zero chunk is skipped")
	assert.Equal(t, uint64(0), frames[0].Offset)
	assert.Equal(t, uint64(2*TransferChunkBytes), frames[1].Offset)
	assert.Equal(t, uint64(3*TransferChunkBytes), frames[2].Offset)
	assert.Len(t, frames[2].Data, 512, "final partial chunk")
	assert.Equal(t, byte(3), frames[2].Data[511])
}

func TestDigestIsFramingIndependent(t *testing.T) {
	path, content := testDevice(t)
	size := uint64(len(content))

	_, skipping, err := ReadDevice(t.Context(), path, func(Frame) error { return nil })
	require.NoError(t, err)

	// a sender that transmits every chunk, zeros included
	sendingAll := NewDigestBuilder()
	for offset := 0; offset < len(content); offset += TransferChunkBytes {
		end := min(offset+TransferChunkBytes, len(content))
		sendingAll.AddChunk(content[offset:end])
	}

	assert.Equal(t, skipping, sendingAll.Sum(size))
}

func TestDigestBindsSize(t *testing.T) {
	first := NewDigestBuilder()
	first.AddZeroChunk(TransferChunkBytes)
	second := NewDigestBuilder()
	second.AddZeroChunk(TransferChunkBytes)

	assert.NotEqual(t, first.Sum(TransferChunkBytes), second.Sum(2*TransferChunkBytes))
}

func TestReadDeviceAllZeroSendsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device")
	require.NoError(t, os.WriteFile(path, make([]byte, 2*TransferChunkBytes), 0o600))

	_, _, err := ReadDevice(t.Context(), path, func(Frame) error {
		t.Fatal("no frame expected")
		return nil
	})

	require.NoError(t, err)
}

func TestReadDeviceStopsOnCancel(t *testing.T) {
	path, _ := testDevice(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := ReadDevice(ctx, path, func(Frame) error { return nil })

	assert.ErrorIs(t, err, context.Canceled)
}
