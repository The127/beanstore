package api

import (
	"context"
	"testing"

	runner "github.com/The127/go-runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/lvm"
)

const scanLVs = `{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi-a-tz--",
	 "lv_tags": "beanstore.state=creating", "pool_lv": "pool0", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "0.00",
	 "metadata_percent": "", "lv_active": "", "lv_layout": "thin,sparse"}
]}], "log": []}`

type fakeRunner struct{ output string }

func (f *fakeRunner) Run(_ context.Context, _ *runner.Cmd) ([]byte, error) {
	return []byte(f.output), nil
}

func TestListVolumesMapsToProto(t *testing.T) {
	server := &volumeServiceServer{
		lvm: lvm.New(lvm.WithRunner(&fakeRunner{output: scanLVs})),
		cfg: config.Config{VolumeGroup: "vg0", ThinPool: "pool0"},
	}

	response, err := server.ListVolumes(t.Context(), &beanstorev1.ListVolumesRequest{})

	require.NoError(t, err)
	require.Len(t, response.Volumes, 1)
	assert.Equal(t, "vol-1", response.Volumes[0].VolumeId)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_CREATING, response.Volumes[0].State)
	assert.Equal(t, uint64(1<<30), response.Volumes[0].SizeBytes)
	assert.Zero(t, response.Volumes[0].UsedBytes)
}
