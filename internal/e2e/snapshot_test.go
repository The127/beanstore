//go:build integration

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
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

func TestIntegrationSnapshotExportRace(t *testing.T) {
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
	pattern := patternFile(t, 8<<20, 251)
	require.NoError(t, sudoRun(ctx, "dd", "if="+pattern.path, "of="+attach.DevicePath, "bs=1M", "conv=fsync"))

	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	require.NoError(t, err)

	// detached, so only the export pin can refuse the deletes below
	_, err = node.volumes.Detach(ctx, &beanstorev1.DetachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	require.NoError(t, node.lvm.ActivateLogicalVolume(ctx, lvm.Name(node.vg+"/snap-1"),
		lvm.ActivateLogicalVolumeOptions{IgnoreActivationSkip: true}))
	require.NoError(t, sudoRun(ctx, "chmod", "o+r", "/dev/"+node.vg+"/snap-1"))

	// fixed small windows keep the server blocked mid-stream, so the
	// export provably stays live while the deletes fire
	slow, err := grpc.NewClient(node.address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithInitialWindowSize(64<<10),
		grpc.WithInitialConnWindowSize(64<<10))
	require.NoError(t, err)
	t.Cleanup(func() { _ = slow.Close() })

	stream, err := beanstorev1.NewVolumeServiceClient(slow).Export(ctx,
		&beanstorev1.ExportRequest{SnapshotId: "snap-1"})
	require.NoError(t, err)
	first, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, first.GetFrame(), "the pattern produces frames")

	_, err = node.volumes.DeleteSnapshot(ctx, &beanstorev1.DeleteSnapshotRequest{SnapshotId: "snap-1"})
	assert.Equal(t, codes.Aborted, status.Code(err), "the live export blocks the delete")

	_, err = node.volumes.DeleteVolume(ctx, &beanstorev1.DeleteVolumeRequest{
		VolumeId:    "vol-1",
		OperationId: "op-delete-pinned",
		Force:       true,
	})
	assert.Equal(t, codes.Aborted, status.Code(err), "the live export blocks the cascade")

	// a second full export runs concurrently and releases its own pin
	_, concurrentTrailer := export(t, node, "snap-1")

	expected := make([]byte, 16<<20)
	copy(expected, pattern.bytes)
	assert.Equal(t, expectedDigest(expected), concurrentTrailer.Digest)

	_, err = node.volumes.DeleteSnapshot(ctx, &beanstorev1.DeleteSnapshotRequest{SnapshotId: "snap-1"})
	assert.Equal(t, codes.Aborted, status.Code(err), "the first export still holds its pin")

	// drain the slow export, its content matches despite the deletes
	var slowTrailer *beanstorev1.ExportTrailer
	for slowTrailer == nil {
		response, err := stream.Recv()
		require.NoError(t, err)
		slowTrailer = response.GetTrailer()
	}
	assert.Equal(t, expectedDigest(expected), slowTrailer.Digest)

	_, err = node.volumes.DeleteSnapshot(ctx, &beanstorev1.DeleteSnapshotRequest{SnapshotId: "snap-1"})
	require.NoError(t, err, "the last pin released the snapshot")

	_, err = node.volumes.DeleteVolume(ctx, &beanstorev1.DeleteVolumeRequest{
		VolumeId:    "vol-1",
		OperationId: "op-delete",
	})
	require.NoError(t, err)
	waitDone(t, node.operations, "op-delete")
}

func TestIntegrationSnapshotCopy(t *testing.T) {
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
	pattern := patternFile(t, 3<<20, 239)
	require.NoError(t, sudoRun(ctx, "dd", "if="+pattern.path, "of="+attach.DevicePath, "bs=1M", "conv=fsync"))

	_, err = source.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	require.NoError(t, err)

	// snapshots have no authority to move, PushVolume refuses them
	_, err = source.volumes.PushVolume(ctx, &beanstorev1.PushVolumeRequest{
		VolumeId:      "snap-1",
		TransferId:    "tr-refused",
		TargetAddress: target.address,
		OperationId:   "op-push-snapshot",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	wrongState, ok := client.WrongState(err)
	require.True(t, ok)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_SNAPSHOT, wrongState.Found)

	// the copy path that works today: the caller pumps export frames
	// into the target's transfer plane
	frames, trailer := export(t, source, "snap-1")

	_, err = target.volumes.PrepareReceive(ctx, &beanstorev1.PrepareReceiveRequest{
		VolumeId:   "vol-copy",
		SizeBytes:  trailer.SizeBytes,
		TransferId: "tr-copy",
	})
	require.NoError(t, err)
	require.NoError(t, sudoRun(ctx, "chmod", "o+rw", "/dev/"+target.vg+"/vol-copy"))

	stream, err := target.transfers.Receive(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&beanstorev1.ReceiveRequest{
		Content: &beanstorev1.ReceiveRequest_Header{Header: &beanstorev1.ReceiveHeader{TransferId: "tr-copy"}},
	}))
	for _, frame := range frames {
		require.NoError(t, stream.Send(&beanstorev1.ReceiveRequest{
			Content: &beanstorev1.ReceiveRequest_Frame{Frame: frame},
		}))
	}
	_, err = stream.CloseAndRecv()
	require.NoError(t, err)
	_, err = target.transfers.CommitTransfer(ctx, &beanstorev1.CommitTransferRequest{
		TransferId: "tr-copy",
		Digest:     trailer.Digest,
	})
	require.NoError(t, err)

	// the snapshot materialized as a full standalone volume
	list, err := target.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Volumes, 1)
	assert.Equal(t, "vol-copy", list.Volumes[0].VolumeId)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_READY, list.Volumes[0].State)
	assert.Equal(t, uint64(16<<20), list.Volumes[0].SizeBytes)
	assert.Empty(t, list.Volumes[0].OriginId, "the copy has no origin link")

	_, err = target.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-copy",
		SnapshotId: "snap-copy",
	})
	require.NoError(t, err)
	_, copyTrailer := export(t, target, "snap-copy")
	assert.Equal(t, trailer.Digest, copyTrailer.Digest, "the copy carries the snapshot content")

	// a copy moves nothing, the source keeps volume and snapshot
	list, err = source.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	states := map[string]beanstorev1.VolumeState{}
	for _, volume := range list.Volumes {
		states[volume.VolumeId] = volume.State
	}
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_ATTACHED, states["vol-1"])
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_SNAPSHOT, states["snap-1"])
}

func TestIntegrationSnapshotRecovery(t *testing.T) {
	lvmClient, cfg := provision(t)
	ctx := t.Context()

	require.NoError(t, lvmClient.CreateThinVolume(ctx, cfg.VolumeGroup, cfg.ThinPool,
		"vol-1", 16<<20, lvm.CreateThinVolumeOptions{
			AddTags:  []string{storage.StateTag(storage.StateReady)},
			Activate: lvm.Bool(false),
		}))
	require.NoError(t, storage.CreateSnapshot(ctx, lvmClient, cfg, "vol-1", "snap-1"))

	// a crash mid-export leaves the snapshot active
	require.NoError(t, lvmClient.ActivateLogicalVolume(ctx, lvm.Name(cfg.VolumeGroup+"/snap-1"),
		lvm.ActivateLogicalVolumeOptions{IgnoreActivationSkip: true}))

	require.NoError(t, storage.Recover(ctx, lvmClient, cfg))

	volumes, err := storage.ListVolumes(ctx, lvmClient, cfg)
	require.NoError(t, err)
	states := map[string]storage.Volume{}
	for _, volume := range volumes {
		states[volume.ID] = volume
	}
	require.Contains(t, states, "snap-1")
	assert.False(t, states["snap-1"].Active, "recovery deactivated the stray snapshot")
	assert.Equal(t, storage.StateSnapshot, states["snap-1"].State, "the snapshot itself survives")
	assert.Equal(t, storage.StateReady, states["vol-1"].State)
}
