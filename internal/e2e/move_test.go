//go:build integration

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
