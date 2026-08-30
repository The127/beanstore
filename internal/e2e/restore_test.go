//go:build integration

package e2e

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
)

// readDevice reads size bytes from the attached device.
func readDevice(t *testing.T, path string, size int) []byte {
	t.Helper()

	device, err := os.Open(path) //nolint:gosec // test device node
	require.NoError(t, err)
	defer func() {
		_ = device.Close()
	}()

	content := make([]byte, size)
	_, err = device.ReadAt(content, 0)
	require.NoError(t, err)

	return content
}

func TestIntegrationCreateVolumeFromSnapshot(t *testing.T) {
	node := daemon(t)
	ctx := t.Context()

	_, err := node.volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-1",
		SizeBytes:   16 << 20,
		OperationId: "op-create",
	})
	require.NoError(t, err)
	waitDone(t, node.operations, "op-create")

	attach, err := node.volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)
	pattern := patternFile(t, 2<<20, 229)
	require.NoError(t, sudoRun(ctx, "dd", "if="+pattern.path, "of="+attach.DevicePath, "bs=1M", "conv=fsync"))

	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	require.NoError(t, err)

	_, err = node.volumes.CreateVolumeFromSnapshot(ctx, &beanstorev1.CreateVolumeFromSnapshotRequest{
		VolumeId:         "vol-copy",
		SourceSnapshotId: "snap-1",
		OperationId:      "op-copy",
	})
	require.NoError(t, err)
	waitDone(t, node.operations, "op-copy")

	list, err := node.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	states := map[string]*beanstorev1.Volume{}
	for _, volume := range list.Volumes {
		states[volume.VolumeId] = volume
	}
	require.Contains(t, states, "vol-copy")
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_READY, states["vol-copy"].State)
	assert.Equal(t, uint64(16<<20), states["vol-copy"].SizeBytes)
	assert.Empty(t, states["vol-copy"].OriginId, "the copy is standalone")

	// the copy survives its source's deletion and carries the content
	_, err = node.volumes.DeleteSnapshot(ctx, &beanstorev1.DeleteSnapshotRequest{SnapshotId: "snap-1"})
	require.NoError(t, err)

	copyAttach, err := node.volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-copy"})
	require.NoError(t, err)
	assert.Equal(t, pattern.bytes, readDevice(t, copyAttach.DevicePath, len(pattern.bytes)))
}

func TestIntegrationRestoreEdges(t *testing.T) {
	node := daemon(t)
	ctx := t.Context()

	for _, id := range []string{"vol-1", "vol-2"} {
		_, err := node.volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
			VolumeId:    id,
			SizeBytes:   16 << 20,
			OperationId: "op-create-" + id,
		})
		require.NoError(t, err)
		waitDone(t, node.operations, "op-create-"+id)
	}
	_, err := node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	require.NoError(t, err)
	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-sib",
	})
	require.NoError(t, err)
	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-2",
		SnapshotId: "snap-other",
	})
	require.NoError(t, err)

	// rollback refusals
	_, err = node.volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)
	_, err = node.volumes.RollbackVolume(ctx, &beanstorev1.RollbackVolumeRequest{
		VolumeId: "vol-1", SourceSnapshotId: "snap-1",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "rollback is detached only")
	_, err = node.volumes.Detach(ctx, &beanstorev1.DetachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	_, err = node.volumes.RollbackVolume(ctx, &beanstorev1.RollbackVolumeRequest{
		VolumeId: "vol-1", SourceSnapshotId: "snap-other",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.ErrorContains(t, err, "does not belong")

	_, err = node.volumes.RollbackVolume(ctx, &beanstorev1.RollbackVolumeRequest{
		VolumeId: "vol-1", SourceSnapshotId: "snap-9",
	})
	assert.Equal(t, codes.NotFound, status.Code(err))

	// copy refusals
	_, err = node.volumes.CreateVolumeFromSnapshot(ctx, &beanstorev1.CreateVolumeFromSnapshotRequest{
		VolumeId: "vol-2", SourceSnapshotId: "snap-1", OperationId: "op-taken",
	})
	assert.Equal(t, codes.AlreadyExists, status.Code(err), "the volume name is taken")

	_, err = node.volumes.CreateVolumeFromSnapshot(ctx, &beanstorev1.CreateVolumeFromSnapshotRequest{
		VolumeId: "vol-3", SourceSnapshotId: "vol-2", OperationId: "op-from-volume",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "only snapshots copy")

	// rollback leaves the sibling snapshot first class
	_, err = node.volumes.RollbackVolume(ctx, &beanstorev1.RollbackVolumeRequest{
		VolumeId: "vol-1", SourceSnapshotId: "snap-1",
	})
	require.NoError(t, err)

	list, err := node.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	states := map[string]*beanstorev1.Volume{}
	for _, volume := range list.Volumes {
		states[volume.VolumeId] = volume
	}
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_SNAPSHOT, states["snap-sib"].State)
	assert.Equal(t, "vol-1", states["snap-sib"].OriginId, "the sibling keeps its lineage")

	_, err = node.volumes.DeleteVolume(ctx, &beanstorev1.DeleteVolumeRequest{
		VolumeId:    "vol-1",
		OperationId: "op-delete-guarded",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err),
		"the siblings still guard the rolled back volume")
}

func TestIntegrationRollbackVolume(t *testing.T) {
	node := daemon(t)
	ctx := t.Context()

	_, err := node.volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-1",
		SizeBytes:   16 << 20,
		OperationId: "op-create",
	})
	require.NoError(t, err)
	waitDone(t, node.operations, "op-create")

	attach, err := node.volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)
	before := patternFile(t, 2<<20, 227)
	require.NoError(t, sudoRun(ctx, "dd", "if="+before.path, "of="+attach.DevicePath, "bs=1M", "conv=fsync"))

	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	require.NoError(t, err)

	after := patternFile(t, 2<<20, 223)
	require.NoError(t, sudoRun(ctx, "dd", "if="+after.path, "of="+attach.DevicePath, "bs=1M", "conv=fsync"))
	_, err = node.volumes.Detach(ctx, &beanstorev1.DetachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	_, err = node.volumes.RollbackVolume(ctx, &beanstorev1.RollbackVolumeRequest{
		VolumeId:         "vol-1",
		SourceSnapshotId: "snap-1",
	})
	require.NoError(t, err)

	attach, err = node.volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)
	assert.Equal(t, before.bytes, readDevice(t, attach.DevicePath, len(before.bytes)),
		"the volume carries the snapshot content")

	// the snapshot survives, rolling back again works
	list, err := node.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	states := map[string]*beanstorev1.Volume{}
	for _, volume := range list.Volumes {
		states[volume.VolumeId] = volume
	}
	require.Contains(t, states, "snap-1")
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_SNAPSHOT, states["snap-1"].State)
	assert.Equal(t, "vol-1", states["snap-1"].OriginId, "the lineage tag survived")

	require.NoError(t, sudoRun(ctx, "dd", "if="+after.path, "of="+attach.DevicePath, "bs=1M", "conv=fsync"))
	_, err = node.volumes.Detach(ctx, &beanstorev1.DetachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	_, err = node.volumes.RollbackVolume(ctx, &beanstorev1.RollbackVolumeRequest{
		VolumeId:         "vol-1",
		SourceSnapshotId: "snap-1",
	})
	require.NoError(t, err, "repeated rollback to the same snapshot")

	attach, err = node.volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)
	assert.Equal(t, before.bytes, readDevice(t, attach.DevicePath, len(before.bytes)))
}
