//go:build integration

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/storage"
)

// expectedDigest computes the transfer digest of the full content.
func expectedDigest(content []byte) []byte {
	builder := storage.NewDigestBuilder()
	for offset := 0; offset < len(content); offset += storage.TransferChunkBytes {
		builder.AddChunk(content[offset : offset+storage.TransferChunkBytes])
	}

	return builder.Sum(uint64(len(content)))
}

func TestIntegrationSnapshotPointInTime(t *testing.T) {
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

	first := patternFile(t, 2<<20, 251)
	require.NoError(t, sudoRun(ctx, "dd", "if="+first.path, "of="+attach.DevicePath, "bs=1M", "conv=fsync"))

	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	require.NoError(t, err)

	// writes after the snapshot land at 4MiB, past the first pattern
	second := patternFile(t, 1<<20, 241)
	require.NoError(t, sudoRun(ctx, "dd", "if="+second.path, "of="+attach.DevicePath,
		"bs=1M", "seek=4", "conv=fsync"))

	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-2",
	})
	require.NoError(t, err)

	beforeWrite := make([]byte, 16<<20)
	copy(beforeWrite, first.bytes)
	afterWrite := make([]byte, 16<<20)
	copy(afterWrite, first.bytes)
	copy(afterWrite[4<<20:], second.bytes)

	_, firstTrailer := export(t, node, "snap-1")
	assert.Equal(t, expectedDigest(beforeWrite), firstTrailer.Digest,
		"the later write is invisible in the first snapshot")

	_, secondTrailer := export(t, node, "snap-2")
	assert.Equal(t, expectedDigest(afterWrite), secondTrailer.Digest,
		"the second snapshot carries both writes")

	// the origin keeps its data through snapshot deletion
	_, err = node.volumes.DeleteSnapshot(ctx, &beanstorev1.DeleteSnapshotRequest{SnapshotId: "snap-1"})
	require.NoError(t, err)
	_, err = node.volumes.Detach(ctx, &beanstorev1.DetachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-3",
	})
	require.NoError(t, err)
	_, thirdTrailer := export(t, node, "snap-3")
	assert.Equal(t, expectedDigest(afterWrite), thirdTrailer.Digest,
		"the origin is intact after deleting a snapshot")

	_, err = node.volumes.DeleteVolume(ctx, &beanstorev1.DeleteVolumeRequest{
		VolumeId:    "vol-1",
		OperationId: "op-delete",
		Force:       true,
	})
	require.NoError(t, err)
	waitDone(t, node.operations, "op-delete")

	list, err := node.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	assert.Empty(t, list.Volumes)
}
