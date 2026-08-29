//go:build integration

package e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runner "github.com/The127/go-runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/The127/beanstore/client"
	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/api"
	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/internal/storage"
	"github.com/The127/beanstore/lvm"
)

// sudoRunner elevates commands so the test process itself can stay
// unprivileged, mirroring the lvm integration harness.
type sudoRunner struct{}

var realRunner = runner.New()

func (sudoRunner) Run(ctx context.Context, cmd *runner.Cmd) ([]byte, error) {
	elevated := runner.Command("sudo", append([]string{"-n", cmd.Name()}, cmd.Args()...)...)
	return realRunner.Run(ctx, elevated)
}

func sudoRun(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n"}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sudo %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}

	return nil
}

func loopDevice(t *testing.T) lvm.Device {
	t.Helper()

	ctx := t.Context()

	err := sudoRun(ctx, "true")
	if err != nil {
		t.Skip("passwordless sudo unavailable, skipping daemon integration test")
	}

	backing := filepath.Join(t.TempDir(), "backing.img")
	require.NoError(t, os.WriteFile(backing, nil, 0o600))
	require.NoError(t, os.Truncate(backing, 1<<30))

	cmd := exec.CommandContext(ctx, "sudo", "-n", "losetup", "--find", "--show", backing)
	loopOut, err := cmd.Output()
	require.NoError(t, err)
	loop := lvm.Device(strings.TrimSpace(string(loopOut)))
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_ = sudoRun(context.Background(), "losetup", "-d", string(loop))
	})

	return loop
}

// testDaemon is a running daemon stack plus the handles the tests
// drive it with.
type testDaemon struct {
	volumes    beanstorev1.VolumeServiceClient
	operations beanstorev1.OperationServiceClient
	transfers  beanstorev1.TransferServiceClient
	vg         string
	lvm        *lvm.Client
}

// daemon brings up the full stack on a real vg: storage setup with
// pool bootstrap, api, grpc over localhost.
func daemon(t *testing.T) testDaemon {
	t.Helper()

	loop := loopDevice(t)
	client := lvm.New(lvm.WithRunner(sudoRunner{}), lvm.WithDevices(loop))
	ctx := t.Context()

	vg := fmt.Sprintf("beanstore-e2e-%d", os.Getpid())
	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, lvm.CreatePhysicalVolumeOptions{}))
	require.NoError(t, client.CreateVolumeGroup(ctx, vg, []lvm.Device{loop}, lvm.CreateVolumeGroupOptions{}))
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_ = client.RemoveVolumeGroup(context.Background(), vg, lvm.RemoveVolumeGroupOptions{Force: true})
	})

	cfg := config.Config{
		VolumeGroup:         vg,
		ThinPool:            "pool0",
		CreatePool:          true,
		PoolSize:            config.PoolSize{Bytes: 256 << 20},
		MaxInboundTransfers: 2,
		TransferGrace:       time.Minute,
	}
	require.NoError(t, storage.Setup(ctx, client, cfg))

	server := grpc.NewServer()
	api.Register(ctx, server, client, cfg)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
	})

	return testDaemon{
		volumes:    beanstorev1.NewVolumeServiceClient(conn),
		operations: beanstorev1.NewOperationServiceClient(conn),
		transfers:  beanstorev1.NewTransferServiceClient(conn),
		vg:         vg,
		lvm:        client,
	}
}

func waitDone(t *testing.T, operations beanstorev1.OperationServiceClient, id string) {
	t.Helper()

	require.Eventually(t, func() bool {
		response, err := operations.GetOperation(t.Context(), &beanstorev1.GetOperationRequest{OperationId: id})
		if err != nil {
			return false
		}
		if failed := response.GetFailed(); failed != nil {
			t.Fatalf("operation %s failed: %s", id, failed.Reason)
		}

		return response.GetDone() != nil
	}, 30*time.Second, 100*time.Millisecond)
}

func TestIntegrationRecovery(t *testing.T) {
	loop := loopDevice(t)
	client := lvm.New(lvm.WithRunner(sudoRunner{}), lvm.WithDevices(loop))
	ctx := t.Context()

	vg := fmt.Sprintf("beanstore-recover-%d", os.Getpid())
	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, lvm.CreatePhysicalVolumeOptions{}))
	require.NoError(t, client.CreateVolumeGroup(ctx, vg, []lvm.Device{loop}, lvm.CreateVolumeGroupOptions{}))
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_ = client.RemoveVolumeGroup(context.Background(), vg, lvm.RemoveVolumeGroupOptions{Force: true})
	})

	cfg := config.Config{
		VolumeGroup: vg,
		ThinPool:    "pool0",
		CreatePool:  true,
		PoolSize:    config.PoolSize{Bytes: 256 << 20},
	}
	require.NoError(t, storage.Setup(ctx, client, cfg))

	// crash leftovers: an unfinished creation and an attached volume
	// whose activation was lost
	require.NoError(t, client.CreateThinVolume(ctx, vg, "pool0", "vol-garbage", 16<<20, lvm.CreateThinVolumeOptions{
		AddTags:  []string{storage.StateTag(storage.StateCreating)},
		Activate: lvm.Bool(false),
	}))
	require.NoError(t, client.CreateThinVolume(ctx, vg, "pool0", "vol-attached", 16<<20, lvm.CreateThinVolumeOptions{
		AddTags:  []string{storage.StateTag(storage.StateAttached)},
		Activate: lvm.Bool(false),
	}))
	require.NoError(t, client.CreateThinVolume(ctx, vg, "pool0", "vol-incoming", 16<<20, lvm.CreateThinVolumeOptions{
		AddTags:  []string{storage.StateTag(storage.StateIncoming), "beanstore.transfer=tr-9"},
		Activate: lvm.Bool(false),
	}))

	require.NoError(t, storage.Recover(ctx, client, cfg))

	volumes, err := storage.ListVolumes(ctx, client, cfg)
	require.NoError(t, err)
	require.Len(t, volumes, 1, "the creating and incoming volumes are gone")
	assert.Equal(t, "vol-attached", volumes[0].ID)
	assert.NotEmpty(t, volumes[0].Path, "re-activated")
}

func TestIntegrationExport(t *testing.T) {
	node := daemon(t)
	volumes, operations, vg, lvmClient := node.volumes, node.operations, node.vg, node.lvm
	ctx := t.Context()

	_, err := volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-1",
		SizeBytes:   16 << 20,
		OperationId: "op-create",
	})
	require.NoError(t, err)
	waitDone(t, operations, "op-create")

	attach, err := volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	// a 2MiB pattern at the device start, the rest stays unwritten
	pattern := filepath.Join(t.TempDir(), "pattern")
	patternBytes := make([]byte, 2<<20)
	for i := range patternBytes {
		patternBytes[i] = byte(i % 251)
	}
	require.NoError(t, os.WriteFile(pattern, patternBytes, 0o600))
	require.NoError(t, sudoRun(ctx, "dd", "if="+pattern, "of="+attach.DevicePath, "bs=1M", "conv=fsync"))

	_, err = volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	require.NoError(t, err)

	// the in-process daemon is unprivileged, unlike a root daemon, so
	// the snapshot's device node needs explicit read access
	require.NoError(t, lvmClient.ActivateLogicalVolume(ctx, lvm.Name(vg+"/snap-1"), lvm.ActivateLogicalVolumeOptions{
		IgnoreActivationSkip: true,
	}))
	require.NoError(t, sudoRun(ctx, "chmod", "o+r", "/dev/"+vg+"/snap-1"))

	stream, err := volumes.Export(ctx, &beanstorev1.ExportRequest{SnapshotId: "snap-1"})
	require.NoError(t, err)

	var trailer *beanstorev1.ExportTrailer
	content := make([]byte, 16<<20)
	for {
		response, err := stream.Recv()
		if err != nil {
			t.Fatalf("stream ended without trailer: %v", err)
		}
		if trailer = response.GetTrailer(); trailer != nil {
			break
		}

		frame := response.GetFrame()
		require.NotNil(t, frame)
		copy(content[frame.Offset:], frame.Data)
	}

	assert.Equal(t, uint64(16<<20), trailer.SizeBytes)
	assert.Equal(t, patternBytes, content[:2<<20], "the pattern survives the export")
	assert.Equal(t, make([]byte, 1<<20), content[15<<20:], "unwritten space is zeros")

	rebuilt := storage.NewDigestBuilder()
	for offset := 0; offset < len(content); offset += storage.TransferChunkBytes {
		rebuilt.AddChunk(content[offset : offset+storage.TransferChunkBytes])
	}
	assert.Equal(t, trailer.Digest, rebuilt.Sum(uint64(len(content))), "the digest matches the reassembled content")

	_, err = volumes.DeleteSnapshot(ctx, &beanstorev1.DeleteSnapshotRequest{SnapshotId: "snap-1"})
	require.NoError(t, err, "the finished export released the snapshot")
}

// export pulls the snapshot's full frame stream after granting the
// unprivileged daemon device read access.
func export(t *testing.T, node testDaemon, snapshotID string) ([]*beanstorev1.Frame, *beanstorev1.ExportTrailer) {
	t.Helper()
	ctx := t.Context()

	require.NoError(t, node.lvm.ActivateLogicalVolume(ctx, lvm.Name(node.vg+"/"+snapshotID), lvm.ActivateLogicalVolumeOptions{
		IgnoreActivationSkip: true,
	}))
	require.NoError(t, sudoRun(ctx, "chmod", "o+r", "/dev/"+node.vg+"/"+snapshotID))

	stream, err := node.volumes.Export(ctx, &beanstorev1.ExportRequest{SnapshotId: snapshotID})
	require.NoError(t, err)

	var frames []*beanstorev1.Frame
	for {
		response, err := stream.Recv()
		require.NoError(t, err, "stream ended without trailer")
		if trailer := response.GetTrailer(); trailer != nil {
			return frames, trailer
		}
		frames = append(frames, response.GetFrame())
	}
}

func TestIntegrationTransferLoopback(t *testing.T) {
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

	pattern := filepath.Join(t.TempDir(), "pattern")
	patternBytes := make([]byte, 3<<20)
	for i := range patternBytes {
		patternBytes[i] = byte(i % 253)
	}
	require.NoError(t, os.WriteFile(pattern, patternBytes, 0o600))
	require.NoError(t, sudoRun(ctx, "dd", "if="+pattern, "of="+attach.DevicePath, "bs=1M", "conv=fsync"))

	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	require.NoError(t, err)
	frames, trailer := export(t, node, "snap-1")

	_, err = node.volumes.PrepareReceive(ctx, &beanstorev1.PrepareReceiveRequest{
		VolumeId:   "vol-1",
		SizeBytes:  trailer.SizeBytes,
		TransferId: "tr-collide",
	})
	assert.Equal(t, codes.AlreadyExists, status.Code(err), "the lv name is taken")

	_, err = node.volumes.PrepareReceive(ctx, &beanstorev1.PrepareReceiveRequest{
		VolumeId:   "vol-2",
		SizeBytes:  trailer.SizeBytes,
		TransferId: "tr-1",
	})
	require.NoError(t, err)
	// the unprivileged daemon needs write access to the incoming device
	require.NoError(t, sudoRun(ctx, "chmod", "o+rw", "/dev/"+node.vg+"/vol-2"))

	query, err := node.transfers.QueryTransfer(ctx, &beanstorev1.QueryTransferRequest{TransferId: "tr-1"})
	require.NoError(t, err)
	assert.Zero(t, query.NextOffset)

	stream, err := node.transfers.Receive(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&beanstorev1.ReceiveRequest{
		Content: &beanstorev1.ReceiveRequest_Header{Header: &beanstorev1.ReceiveHeader{TransferId: "tr-1"}},
	}))
	for _, frame := range frames {
		require.NoError(t, stream.Send(&beanstorev1.ReceiveRequest{
			Content: &beanstorev1.ReceiveRequest_Frame{Frame: frame},
		}))
	}
	_, err = stream.CloseAndRecv()
	require.NoError(t, err)

	_, err = node.transfers.CommitTransfer(ctx, &beanstorev1.CommitTransferRequest{
		TransferId: "tr-1",
		Digest:     trailer.Digest,
	})
	require.NoError(t, err)

	list, err := node.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	states := map[string]beanstorev1.VolumeState{}
	for _, volume := range list.Volumes {
		states[volume.VolumeId] = volume.State
	}
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_READY, states["vol-2"])

	// the content digest is framing independent, re-exporting the copy
	// must reproduce it exactly
	_, err = node.volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-2",
		SnapshotId: "snap-2",
	})
	require.NoError(t, err)
	_, copyTrailer := export(t, node, "snap-2")
	assert.Equal(t, trailer.Digest, copyTrailer.Digest, "the received volume matches the source")

	_, err = node.transfers.AbortTransfer(ctx, &beanstorev1.AbortTransferRequest{TransferId: "tr-1"})
	require.NoError(t, err, "abort is idempotent")

	_, err = node.transfers.QueryTransfer(ctx, &beanstorev1.QueryTransferRequest{TransferId: "tr-1"})
	assert.Equal(t, codes.NotFound, status.Code(err), "finished transfers are dead")
}

func TestIntegrationDaemonLifecycle(t *testing.T) {
	node := daemon(t)
	volumes, operations := node.volumes, node.operations
	ctx := t.Context()

	_, err := volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-1",
		SizeBytes:   64 << 20,
		OperationId: "op-create",
	})
	require.NoError(t, err)
	waitDone(t, operations, "op-create")

	list, err := volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Volumes, 1)
	assert.Equal(t, "vol-1", list.Volumes[0].VolumeId)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_READY, list.Volumes[0].State)
	assert.Equal(t, uint64(64<<20), list.Volumes[0].SizeBytes)

	nodeStatus, err := volumes.GetNodeStatus(ctx, &beanstorev1.GetNodeStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, uint64(256<<20), nodeStatus.PoolSizeBytes)
	assert.Equal(t, uint64(64<<20), nodeStatus.CommittedBytes)
	assert.Equal(t, map[string]uint32{"ready": 1}, nodeStatus.VolumeCounts)
	assert.NotEmpty(t, nodeStatus.LvmVersion)

	_, err = volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-1",
		SizeBytes:   64 << 20,
		OperationId: "op-create",
	})
	assert.Equal(t, codes.AlreadyExists, status.Code(err), "volume and operation id are both taken")

	_, err = volumes.ResizeVolume(ctx, &beanstorev1.ResizeVolumeRequest{
		VolumeId:  "vol-1",
		SizeBytes: 96 << 20,
	})
	require.NoError(t, err)

	list, err = volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	assert.Equal(t, uint64(96<<20), list.Volumes[0].SizeBytes)

	_, err = volumes.ResizeVolume(ctx, &beanstorev1.ResizeVolumeRequest{
		VolumeId:  "vol-1",
		SizeBytes: 64 << 20,
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "shrinking is refused")

	attach, err := volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)
	assert.NotEmpty(t, attach.DevicePath)

	_, err = volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.ErrorContains(t, err, "attached")
	wrongState, ok := client.WrongState(err)
	require.True(t, ok, "the structured detail crosses the wire")
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_ATTACHED, wrongState.Found)

	_, err = volumes.DeleteVolume(ctx, &beanstorev1.DeleteVolumeRequest{
		VolumeId:    "vol-1",
		OperationId: "op-early-delete",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "attached volumes cannot be deleted")

	_, err = volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	require.NoError(t, err, "snapshots of attached volumes work")

	list, err = volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Volumes, 2)
	for _, volume := range list.Volumes {
		if volume.VolumeId == "snap-1" {
			assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_SNAPSHOT, volume.State)
			assert.Equal(t, "vol-1", volume.OriginId)
		}
	}

	_, err = volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "snap-1",
		SnapshotId: "snap-2",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "no snapshots of snapshots")

	_, err = volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "snap-1"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "snapshots are not attachable")

	_, err = volumes.Detach(ctx, &beanstorev1.DetachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	_, err = volumes.DeleteVolume(ctx, &beanstorev1.DeleteVolumeRequest{
		VolumeId:    "vol-1",
		OperationId: "op-refused-delete",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "snapshots block the delete")

	_, err = volumes.DeleteSnapshot(ctx, &beanstorev1.DeleteSnapshotRequest{SnapshotId: "snap-1"})
	require.NoError(t, err)

	_, err = volumes.CreateSnapshot(ctx, &beanstorev1.CreateSnapshotRequest{
		VolumeId:   "vol-1",
		SnapshotId: "snap-1",
	})
	require.NoError(t, err, "snapshots of ready volumes work")

	_, err = volumes.DeleteVolume(ctx, &beanstorev1.DeleteVolumeRequest{
		VolumeId:    "vol-1",
		OperationId: "op-delete",
		Force:       true,
	})
	require.NoError(t, err, "force cascades over the snapshot")
	waitDone(t, operations, "op-delete")

	list, err = volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	assert.Empty(t, list.Volumes)

	_, err = volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	assert.Equal(t, codes.NotFound, status.Code(err))
}
