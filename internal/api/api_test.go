package api

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	runner "github.com/The127/go-runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/internal/operations"
	"github.com/The127/beanstore/lvm"
)

const scanLVs = `{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi-a-tz--",
	 "lv_tags": "beanstore.state=creating", "pool_lv": "pool0", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "0.00",
	 "metadata_percent": "", "lv_active": "", "lv_layout": "thin,sparse"}
]}], "log": []}`

const noLVs = `{"report": [{"lv": []}], "log": []}`

// fakeRunner replays one canned output per lvs call, in order. Other
// commands return nothing, so background work cannot shift the
// sequence.
type fakeRunner struct {
	mu      sync.Mutex
	outputs []string
	served  int
	calls   []*runner.Cmd
}

func (f *fakeRunner) Run(_ context.Context, cmd *runner.Cmd) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, cmd)
	if len(cmd.Args()) == 0 || cmd.Args()[0] != "lvs" || f.served >= len(f.outputs) {
		return nil, nil
	}
	f.served++

	return []byte(f.outputs[f.served-1]), nil
}

func (f *fakeRunner) args(call int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls[call].Args()
}

func testServer(t *testing.T, fake *fakeRunner) (*volumeServiceServer, *operationServiceServer) {
	t.Helper()

	ops := operations.NewTable()
	volumes := &volumeServiceServer{
		lvm:        lvm.New(lvm.WithRunner(fake)),
		cfg:        config.Config{VolumeGroup: "vg0", ThinPool: "pool0"},
		ops:        ops,
		background: t.Context(),
	}

	return volumes, &operationServiceServer{ops: ops}
}

func TestListVolumesMapsToProto(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{scanLVs}})

	response, err := volumes.ListVolumes(t.Context(), &beanstorev1.ListVolumesRequest{})

	require.NoError(t, err)
	require.Len(t, response.Volumes, 1)
	assert.Equal(t, "vol-1", response.Volumes[0].VolumeId)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_CREATING, response.Volumes[0].State)
	assert.Equal(t, uint64(1<<30), response.Volumes[0].SizeBytes)
	assert.Zero(t, response.Volumes[0].UsedBytes)
}

const poolLVs = `{"report": [{"lv": [
	{"lv_name": "pool0", "lv_uuid": "uuid-p", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_metadata_size": "4194304",
	 "lv_attr": "twi-aotz--", "lv_tags": "", "pool_lv": "", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "50.00",
	 "metadata_percent": "25.00", "lv_active": "active",
	 "lv_layout": "pool,thin"}
]}], "log": []}`

func TestGetNodeStatusAggregates(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{poolLVs, scanLVs}})

	response, err := volumes.GetNodeStatus(t.Context(), &beanstorev1.GetNodeStatusRequest{})

	require.NoError(t, err)
	assert.Equal(t, uint64(1<<30), response.PoolSizeBytes)
	assert.Equal(t, uint64(1<<29), response.PoolUsedBytes)
	assert.Equal(t, uint64(4<<20), response.PoolMetadataSizeBytes)
	assert.Equal(t, uint64(1<<20), response.PoolMetadataUsedBytes)
	assert.Equal(t, uint64(1<<30), response.CommittedBytes)
	assert.Equal(t, map[string]uint32{"creating": 1}, response.VolumeCounts)
	assert.NotEmpty(t, response.BeanstoreVersion)
}

func TestCreateVolumeRunsToDone(t *testing.T) {
	fake := &fakeRunner{outputs: []string{noLVs}}
	volumes, operationsServer := testServer(t, fake)

	_, err := volumes.CreateVolume(t.Context(), &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-1",
		SizeBytes:   1 << 30,
		OperationId: "op-1",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		response, err := operationsServer.GetOperation(t.Context(), &beanstorev1.GetOperationRequest{OperationId: "op-1"})
		return err == nil && response.GetDone() != nil
	}, time.Second, time.Millisecond)

	created := strings.Join(fake.args(1), " ")
	assert.Contains(t, created, "lvcreate --type thin --thinpool pool0")
	assert.Contains(t, created, "--addtag beanstore.state=creating")
	assert.Contains(t, created, "-a n")

	readied := strings.Join(fake.args(2), " ")
	assert.Contains(t, readied, "--addtag beanstore.state=ready")
	assert.Contains(t, readied, "--deltag beanstore.state=creating")
	assert.Contains(t, readied, "vg0/vol-1")
}

func TestCreateVolumeRefusesDuplicateOperation(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{noLVs, noLVs}})

	_, err := volumes.CreateVolume(t.Context(), &beanstorev1.CreateVolumeRequest{
		VolumeId: "vol-1", SizeBytes: 1 << 30, OperationId: "op-1",
	})
	require.NoError(t, err)

	_, err = volumes.CreateVolume(t.Context(), &beanstorev1.CreateVolumeRequest{
		VolumeId: "vol-2", SizeBytes: 1 << 30, OperationId: "op-1",
	})

	assert.Equal(t, codes.AlreadyExists, status.Code(err))
	assert.ErrorContains(t, err, "operation id already used")
}

func TestCreateVolumeRefusesExistingVolume(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{scanLVs}})

	_, err := volumes.CreateVolume(t.Context(), &beanstorev1.CreateVolumeRequest{
		VolumeId: "vol-1", SizeBytes: 1 << 30, OperationId: "op-1",
	})

	assert.Equal(t, codes.AlreadyExists, status.Code(err))
	assert.ErrorContains(t, err, "volume already exists")
}

func TestCreateVolumeValidation(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{})

	cases := []*beanstorev1.CreateVolumeRequest{
		{VolumeId: "", SizeBytes: 1, OperationId: "op-1"},
		{VolumeId: "-leading-dash", SizeBytes: 1, OperationId: "op-1"},
		{VolumeId: "vol/1", SizeBytes: 1, OperationId: "op-1"},
		{VolumeId: "vol-1", SizeBytes: 0, OperationId: "op-1"},
		{VolumeId: "vol-1", SizeBytes: 1, OperationId: ""},
	}
	for _, request := range cases {
		_, err := volumes.CreateVolume(t.Context(), request)

		assert.Equal(t, codes.InvalidArgument, status.Code(err), request)
	}
}

const readyLV = `{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=ready", "pool_lv": "pool0", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "",
	 "metadata_percent": "", "lv_active": "", "lv_layout": "thin,sparse"}
]}], "log": []}`

const attachedLV = `{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi-a-tz--",
	 "lv_tags": "beanstore.state=attached", "pool_lv": "pool0", "origin": "",
	 "lv_path": "/dev/vg0/vol-1", "lv_dm_path": "", "data_percent": "1.00",
	 "metadata_percent": "", "lv_active": "active",
	 "lv_layout": "thin,sparse"}
]}], "log": []}`

func TestAttachReturnsDevicePath(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{readyLV, attachedLV}})

	response, err := volumes.Attach(t.Context(), &beanstorev1.AttachRequest{VolumeId: "vol-1"})

	require.NoError(t, err)
	assert.Equal(t, "/dev/vg0/vol-1", response.DevicePath)
}

func TestAttachErrorCodes(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{attachedLV, noLVs}})

	_, err := volumes.Attach(t.Context(), &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.ErrorContains(t, err, "in state attached")

	_, err = volumes.Attach(t.Context(), &beanstorev1.AttachRequest{VolumeId: "vol-9"})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestDetachOnReadyVolumeFails(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{readyLV}})

	_, err := volumes.Detach(t.Context(), &beanstorev1.DetachRequest{VolumeId: "vol-1"})

	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestGetOperationStates(t *testing.T) {
	fake := &fakeRunner{}
	_, operationsServer := testServer(t, fake)

	_, err := operationsServer.GetOperation(t.Context(), &beanstorev1.GetOperationRequest{OperationId: "op-9"})
	assert.Equal(t, codes.NotFound, status.Code(err))

	require.NoError(t, operationsServer.ops.Begin("op-1"))
	response, err := operationsServer.GetOperation(t.Context(), &beanstorev1.GetOperationRequest{OperationId: "op-1"})
	require.NoError(t, err)
	assert.NotNil(t, response.GetPending())

	operationsServer.ops.Fail("op-1", "pool exploded")
	response, err = operationsServer.GetOperation(t.Context(), &beanstorev1.GetOperationRequest{OperationId: "op-1"})
	require.NoError(t, err)
	assert.Equal(t, "pool exploded", response.GetFailed().GetReason())
}
