//go:build integration

package e2e

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/The127/beanstore/client"
	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/storage"
	"github.com/The127/beanstore/lvm"
)

func TestIntegrationMove(t *testing.T) {
	source := daemon(t)
	target := daemon(t)
	ctx := t.Context()

	_, err := source.volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-1",
		SizeBytes:   16 << 20,
		OperationId: "op-create",
	})
	require.NoError(t, err)
	waitDone(t, source.operations, "op-create")

	attach, err := source.volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	pattern := filepath.Join(t.TempDir(), "pattern")
	patternBytes := make([]byte, 3<<20)
	for i := range patternBytes {
		patternBytes[i] = byte(i % 249)
	}
	require.NoError(t, os.WriteFile(pattern, patternBytes, 0o600))
	require.NoError(t, sudoRun(ctx, "dd", "if="+pattern, "of="+attach.DevicePath, "bs=1M", "conv=fsync"))

	// moves are detached only
	_, err = source.volumes.Detach(ctx, &beanstorev1.DetachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	_, err = target.volumes.PrepareReceive(ctx, &beanstorev1.PrepareReceiveRequest{
		VolumeId:   "vol-1",
		SizeBytes:  16 << 20,
		TransferId: "tr-move",
	})
	require.NoError(t, err)

	// the unprivileged daemons need device access a root daemon has
	require.NoError(t, sudoRun(ctx, "chmod", "o+rw", "/dev/"+target.vg+"/vol-1"))
	require.NoError(t, source.lvm.ActivateLogicalVolume(ctx,
		lvm.Name(source.vg+"/vol-1"), lvm.ActivateLogicalVolumeOptions{}))
	require.NoError(t, sudoRun(ctx, "chmod", "o+r", "/dev/"+source.vg+"/vol-1"))

	_, err = source.volumes.PushVolume(ctx, &beanstorev1.PushVolumeRequest{
		VolumeId:      "vol-1",
		TransferId:    "tr-move",
		TargetAddress: target.address,
		OperationId:   "op-push",
	})
	require.NoError(t, err)
	waitDone(t, source.operations, "op-push")

	list, err := source.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Volumes, 1)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_RETIRED, list.Volumes[0].State)

	list, err = target.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Volumes, 1)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_READY, list.Volumes[0].State)
	assert.Equal(t, uint64(16<<20), list.Volumes[0].SizeBytes)

	// re-export the copy and compare against the written content
	_, err = target.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-moved",
	})
	require.NoError(t, err)
	_, trailer := export(t, target, "snap-moved")

	content := make([]byte, 16<<20)
	copy(content, patternBytes)
	expected := storage.NewDigestBuilder()
	for offset := 0; offset < len(content); offset += storage.TransferChunkBytes {
		expected.AddChunk(content[offset : offset+storage.TransferChunkBytes])
	}
	assert.Equal(t, expected.Sum(uint64(len(content))), trailer.Digest,
		"the moved volume holds the written content")

	// the retired source copy deletes like any retired volume
	_, err = source.volumes.DeleteVolume(ctx, &beanstorev1.DeleteVolumeRequest{
		VolumeId:    "vol-1",
		OperationId: "op-delete",
	})
	require.NoError(t, err)
	waitDone(t, source.operations, "op-delete")

	list, err = source.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	assert.Empty(t, list.Volumes)
}

// waitFailed waits for the operation to fail and returns the reason.
func waitFailed(t *testing.T, operations beanstorev1.OperationServiceClient, id string) string {
	t.Helper()

	var reason string
	require.Eventually(t, func() bool {
		response, err := operations.GetOperation(t.Context(), &beanstorev1.GetOperationRequest{OperationId: id})
		if err != nil {
			return false
		}
		if done := response.GetDone(); done != nil {
			t.Fatalf("operation %s succeeded, expected failure", id)
		}
		failed := response.GetFailed()
		if failed == nil {
			return false
		}
		reason = failed.Reason

		return true
	}, 30*time.Second, 100*time.Millisecond)

	return reason
}

func TestIntegrationMoveEdges(t *testing.T) {
	source := daemon(t)
	target := daemon(t)
	ctx := t.Context()

	_, err := source.volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-1",
		SizeBytes:   16 << 20,
		OperationId: "op-create",
	})
	require.NoError(t, err)
	waitDone(t, source.operations, "op-create")

	_, err = source.volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	_, err = source.volumes.PushVolume(ctx, &beanstorev1.PushVolumeRequest{
		VolumeId:      "vol-1",
		TransferId:    "tr-attached",
		TargetAddress: target.address,
		OperationId:   "op-push-attached",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "moves are detached only")
	wrongState, ok := client.WrongState(err)
	require.True(t, ok)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_ATTACHED, wrongState.Found)

	_, err = source.volumes.PushVolume(ctx, &beanstorev1.PushVolumeRequest{
		VolumeId:      "vol-9",
		TransferId:    "tr-unknown",
		TargetAddress: target.address,
		OperationId:   "op-push-unknown",
	})
	assert.Equal(t, codes.NotFound, status.Code(err))

	_, err = source.volumes.PushVolume(ctx, &beanstorev1.PushVolumeRequest{
		VolumeId:      "vol-1",
		TransferId:    "tr-bad-address",
		TargetAddress: "no spaces allowed",
		OperationId:   "op-push-bad-address",
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// the refused push consumed its operation id
	_, err = source.volumes.PushVolume(ctx, &beanstorev1.PushVolumeRequest{
		VolumeId:      "vol-1",
		TransferId:    "tr-attached",
		TargetAddress: target.address,
		OperationId:   "op-push-attached",
	})
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	_, err = source.volumes.Detach(ctx, &beanstorev1.DetachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	// the target never prepared this transfer, the push fails and the
	// volume reverts to READY
	_, err = source.volumes.PushVolume(ctx, &beanstorev1.PushVolumeRequest{
		VolumeId:      "vol-1",
		TransferId:    "tr-unprepared",
		TargetAddress: target.address,
		OperationId:   "op-push-unprepared",
	})
	require.NoError(t, err)
	reason := waitFailed(t, source.operations, "op-push-unprepared")
	assert.Contains(t, reason, "unknown transfer")

	list, err := source.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Volumes, 1)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_READY, list.Volumes[0].State, "the failed push reverted")

	// a reverted volume pushes again with a fresh transfer and operation
	_, err = target.volumes.PrepareReceive(ctx, &beanstorev1.PrepareReceiveRequest{
		VolumeId:   "vol-1",
		SizeBytes:  16 << 20,
		TransferId: "tr-retry",
	})
	require.NoError(t, err)
	require.NoError(t, sudoRun(ctx, "chmod", "o+rw", "/dev/"+target.vg+"/vol-1"))
	require.NoError(t, source.lvm.ActivateLogicalVolume(ctx,
		lvm.Name(source.vg+"/vol-1"), lvm.ActivateLogicalVolumeOptions{}))
	require.NoError(t, sudoRun(ctx, "chmod", "o+r", "/dev/"+source.vg+"/vol-1"))

	_, err = source.volumes.PushVolume(ctx, &beanstorev1.PushVolumeRequest{
		VolumeId:      "vol-1",
		TransferId:    "tr-retry",
		TargetAddress: target.address,
		OperationId:   "op-push-retry",
	})
	require.NoError(t, err)
	waitDone(t, source.operations, "op-push-retry")

	list, err = source.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Volumes, 1)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_RETIRED, list.Volumes[0].State)
}

func TestIntegrationMoveUnreachableTarget(t *testing.T) {
	source := daemon(t)
	ctx := t.Context()

	_, err := source.volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-1",
		SizeBytes:   16 << 20,
		OperationId: "op-create",
	})
	require.NoError(t, err)
	waitDone(t, source.operations, "op-create")

	// reserve a port nobody answers on
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deadAddress := dead.Addr().String()
	require.NoError(t, dead.Close())

	_, err = source.volumes.PushVolume(ctx, &beanstorev1.PushVolumeRequest{
		VolumeId:      "vol-1",
		TransferId:    "tr-nowhere",
		TargetAddress: deadAddress,
		OperationId:   "op-push-nowhere",
	})
	require.NoError(t, err)
	waitFailed(t, source.operations, "op-push-nowhere")

	list, err := source.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Volumes, 1)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_READY, list.Volumes[0].State,
		"the exhausted push reverted")
}

func TestIntegrationMoveCrashResolution(t *testing.T) {
	target := daemon(t)
	ctx := t.Context()

	// the copy whose commit landed before the source "crashed"
	_, err := target.volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-moved",
		SizeBytes:   16 << 20,
		OperationId: "op-create",
	})
	require.NoError(t, err)
	waitDone(t, target.operations, "op-create")

	sourceClient, sourceCfg := provision(t)
	seed := func(name string, state storage.State, transfer string) {
		require.NoError(t, sourceClient.CreateThinVolume(ctx, sourceCfg.VolumeGroup, sourceCfg.ThinPool,
			name, 16<<20, lvm.CreateThinVolumeOptions{
				AddTags: []string{
					storage.StateTag(state),
					"beanstore.transfer=" + transfer,
					"beanstore.target=" + target.address,
				},
				Activate: lvm.Bool(false),
			}))
	}
	seed("vol-moved", storage.StateCommitting, "tr-1")
	seed("vol-lost", storage.StateCommitting, "tr-2")
	seed("vol-mid", storage.StatePushing, "tr-3")

	// the daemon boot sequence: recover, then serve with the resolver
	require.NoError(t, storage.Recover(ctx, sourceClient, sourceCfg))
	source := serve(t, sourceClient, sourceCfg)

	require.Eventually(t, func() bool {
		list, err := source.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
		if err != nil {
			return false
		}
		states := map[string]beanstorev1.VolumeState{}
		for _, volume := range list.Volumes {
			states[volume.VolumeId] = volume.State
		}

		return states["vol-moved"] == beanstorev1.VolumeState_VOLUME_STATE_RETIRED &&
			states["vol-lost"] == beanstorev1.VolumeState_VOLUME_STATE_READY &&
			states["vol-mid"] == beanstorev1.VolumeState_VOLUME_STATE_READY
	}, 30*time.Second, 200*time.Millisecond,
		"landed commit retires, lost commit and interrupted stream revert")
}
