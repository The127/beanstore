package storage

import (
	"context"
	"strings"
	"sync"
	"testing"

	runner "github.com/The127/go-runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/lvm"
)

const oneVG = `{"report": [{"vg": [
	{"vg_name": "vg0", "vg_uuid": "uuid-vg", "vg_size": "10737418240",
	 "vg_free": "10000000000", "vg_extent_size": "4194304",
	 "vg_extent_count": "2560", "vg_free_count": "2384", "pv_count": "1",
	 "lv_count": "0", "snap_count": "0", "vg_missing_pv_count": "0",
	 "vg_tags": "", "vg_attr": "wz--n-", "vg_exported": "",
	 "vg_partial": "", "vg_shared": "", "vg_autoactivation": "enabled"}
]}], "log": []}`

const noVGs = `{"report": [{"vg": []}], "log": []}`

const onePool = `{"report": [{"lv": [
	{"lv_name": "pool0", "lv_uuid": "uuid-p", "vg_name": "vg0",
	 "lv_size": "536870912", "chunk_size": "65536", "lv_attr": "twi-a-tz--",
	 "lv_tags": "", "pool_lv": "", "origin": "", "lv_path": "",
	 "lv_dm_path": "", "data_percent": "0.00", "metadata_percent": "0.98",
	 "lv_active": "active", "lv_layout": "pool,thin"}
]}], "log": []}`

const bigChunkPool = `{"report": [{"lv": [
	{"lv_name": "pool0", "lv_uuid": "uuid-p", "vg_name": "vg0",
	 "lv_size": "536870912", "chunk_size": "196608", "lv_attr": "twi-a-tz--",
	 "lv_tags": "", "pool_lv": "", "origin": "", "lv_path": "",
	 "lv_dm_path": "", "data_percent": "0.00", "metadata_percent": "0.98",
	 "lv_active": "active", "lv_layout": "pool,thin"}
]}], "log": []}`

const oneLinearLV = `{"report": [{"lv": [
	{"lv_name": "pool0", "lv_uuid": "uuid-l", "vg_name": "vg0",
	 "lv_size": "536870912", "lv_attr": "-wi-a-----", "lv_tags": "",
	 "pool_lv": "", "origin": "", "lv_path": "", "lv_dm_path": "",
	 "data_percent": "", "metadata_percent": "",
	 "lv_active": "active", "lv_layout": "linear"}
]}], "log": []}`

const noLVs = `{"report": [{"lv": []}], "log": []}`

// fakeRunner replays one canned output per call, in order.
type fakeRunner struct {
	mu      sync.Mutex
	outputs []string
	calls   []*runner.Cmd
}

func (f *fakeRunner) Run(_ context.Context, cmd *runner.Cmd) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, cmd)
	if len(f.calls) > len(f.outputs) {
		return nil, nil
	}

	return []byte(f.outputs[len(f.calls)-1]), nil
}

// commands snapshots the recorded argv lines.
func (f *fakeRunner) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	lines := make([]string, len(f.calls))
	for i, call := range f.calls {
		lines[i] = strings.Join(call.Args(), " ")
	}

	return lines
}

func testConfig() config.Config {
	return config.Config{VolumeGroup: "vg0", ThinPool: "pool0"}
}

func TestSetupAcceptsExistingPool(t *testing.T) {
	fake := &fakeRunner{outputs: []string{oneVG, onePool}}
	client := lvm.New(lvm.WithRunner(fake))

	err := Setup(t.Context(), client, testConfig())

	require.NoError(t, err)
	assert.Len(t, fake.calls, 2)
}

func TestSetupFailsWithoutVG(t *testing.T) {
	fake := &fakeRunner{outputs: []string{noVGs}}
	client := lvm.New(lvm.WithRunner(fake))

	err := Setup(t.Context(), client, testConfig())

	assert.ErrorContains(t, err, "create it with vgcreate")
}

func TestSetupFailsWhenPoolIsNotAPool(t *testing.T) {
	fake := &fakeRunner{outputs: []string{oneVG, oneLinearLV}}
	client := lvm.New(lvm.WithRunner(fake))

	err := Setup(t.Context(), client, testConfig())

	assert.ErrorContains(t, err, "not a thin pool")
}

func TestSetupFailsWithoutPoolAndBootstrap(t *testing.T) {
	fake := &fakeRunner{outputs: []string{oneVG, noLVs}}
	client := lvm.New(lvm.WithRunner(fake))

	err := Setup(t.Context(), client, testConfig())

	assert.ErrorContains(t, err, "create it or set create_pool")
}

func TestSetupRefusesUndividingChunkSize(t *testing.T) {
	fake := &fakeRunner{outputs: []string{oneVG, bigChunkPool}}
	client := lvm.New(lvm.WithRunner(fake))

	err := Setup(t.Context(), client, testConfig())

	assert.ErrorContains(t, err, "does not divide")
}

func TestSetupCreatesPoolWithBytes(t *testing.T) {
	fake := &fakeRunner{outputs: []string{oneVG, noLVs, "", onePool}}
	client := lvm.New(lvm.WithRunner(fake))
	cfg := testConfig()
	cfg.CreatePool = true
	cfg.PoolSize = config.PoolSize{Bytes: 512 << 20}

	err := Setup(t.Context(), client, cfg)

	require.NoError(t, err)
	require.Len(t, fake.calls, 4)
	created := strings.Join(fake.calls[2].Args(), " ")
	assert.Contains(t, created, "lvcreate --type thin-pool")
	assert.Contains(t, created, "-L 536870912b")
	assert.Contains(t, created, "-n pool0")
}

func TestSetupCreatesPoolWithPercent(t *testing.T) {
	fake := &fakeRunner{outputs: []string{oneVG, noLVs, "", onePool}}
	client := lvm.New(lvm.WithRunner(fake))
	cfg := testConfig()
	cfg.CreatePool = true
	cfg.PoolSize = config.PoolSize{Percent: 90}

	err := Setup(t.Context(), client, cfg)

	require.NoError(t, err)
	require.Len(t, fake.calls, 4)
	// 90 percent of the vg's 10000000000 free bytes
	assert.Contains(t, strings.Join(fake.calls[2].Args(), " "), "-L 9000000000b")
}
