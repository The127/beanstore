//go:build integration

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/The127/beanstore/client"
	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/storage"
	"github.com/The127/beanstore/lvm"
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

func TestIntegrationSnapshotEdges(t *testing.T) {
	lvmClient, cfg := provision(t)
	ctx := t.Context()

	// a retired leftover from a finished move
	require.NoError(t, lvmClient.CreateThinVolume(ctx, cfg.VolumeGroup, cfg.ThinPool,
		"vol-retired", 16<<20, lvm.CreateThinVolumeOptions{
			AddTags:  []string{storage.StateTag(storage.StateRetired)},
			Activate: lvm.Bool(false),
		}))
	node := serve(t, lvmClient, cfg)

	_, err := node.volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-1",
		SizeBytes:   16 << 20,
		OperationId: "op-create",
	})
	require.NoError(t, err)
	waitDone(t, node.operations, "op-create")

	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	require.NoError(t, err)

	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	assert.Equal(t, codes.AlreadyExists, status.Code(err), "the snapshot name is taken")

	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "vol-retired",
	})
	assert.Equal(t, codes.AlreadyExists, status.Code(err), "volume names share the namespace")

	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-retired",
		SnapshotId: "snap-x",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	wrongState, ok := client.WrongState(err)
	require.True(t, ok)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_RETIRED, wrongState.Found)

	_, err = node.volumes.PrepareReceive(ctx, &beanstorev1.PrepareReceiveRequest{
		VolumeId:   "vol-incoming",
		SizeBytes:  16 << 20,
		TransferId: "tr-1",
	})
	require.NoError(t, err)
	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-incoming",
		SnapshotId: "snap-y",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "incoming volumes have no committed content")

	stream, err := node.volumes.Export(ctx, &beanstorev1.ExportRequest{SnapshotId: "snap-unknown"})
	require.NoError(t, err)
	_, err = stream.Recv()
	assert.Equal(t, codes.NotFound, status.Code(err))

	stream, err = node.volumes.Export(ctx, &beanstorev1.ExportRequest{SnapshotId: "vol-1"})
	require.NoError(t, err)
	_, err = stream.Recv()
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "only snapshots export")

	_, err = node.volumes.DeleteSnapshot(ctx, &beanstorev1.DeleteSnapshotRequest{SnapshotId: "vol-1"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "volumes go through DeleteVolume")

	// the origin grows, its snapshot keeps the old size
	_, err = node.volumes.ResizeVolume(ctx, &beanstorev1.ResizeVolumeRequest{
		VolumeId:  "vol-1",
		SizeBytes: 32 << 20,
	})
	require.NoError(t, err)

	list, err := node.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	sizes := map[string]uint64{}
	for _, volume := range list.Volumes {
		sizes[volume.VolumeId] = volume.SizeBytes
	}
	assert.Equal(t, uint64(32<<20), sizes["vol-1"])
	assert.Equal(t, uint64(16<<20), sizes["snap-1"])

	_, trailer := export(t, node, "snap-1")
	assert.Equal(t, uint64(16<<20), trailer.SizeBytes)
	assert.Equal(t, expectedDigest(make([]byte, 16<<20)), trailer.Digest,
		"the never written origin snapshots as zeros")
}
