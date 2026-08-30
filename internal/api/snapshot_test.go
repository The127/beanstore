package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/The127/beanstore/client"
	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/storage"
)

// snapshotLV renders snap-1 with its device at path.
func snapshotLV(path string) string {
	return fmt.Sprintf(`{"report": [{"lv": [
	{"lv_name": "snap-1", "lv_uuid": "uuid-s", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vri-a-tz--",
	 "lv_tags": "beanstore.state=snapshot", "pool_lv": "pool0",
	 "origin": "vol-1", "lv_path": %q, "lv_dm_path": "",
	 "data_percent": "1.00", "metadata_percent": "", "lv_active": "active",
	 "lv_layout": "thin,sparse"}
]}], "log": []}`, path)
}

func TestCreateSnapshotValidation(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{})

	_, err := volumes.CreateSnapshot(t.Context(), &beanstorev1.CreateSnapshotRequest{
		VolumeId: "-bad", SnapshotId: "snap-1",
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = volumes.CreateSnapshot(t.Context(), &beanstorev1.CreateSnapshotRequest{
		VolumeId: "vol-1", SnapshotId: "",
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateSnapshotRefusesTakenName(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{snapshotLV("")}})

	_, err := volumes.CreateSnapshot(t.Context(), &beanstorev1.CreateSnapshotRequest{
		VolumeId: "vol-1", SnapshotId: "snap-1",
	})

	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestCreateSnapshotRefusesWrongOriginState(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{noLVs, snapshotLV("")}})

	_, err := volumes.CreateSnapshot(t.Context(), &beanstorev1.CreateSnapshotRequest{
		VolumeId: "snap-1", SnapshotId: "snap-2",
	})

	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	wrongState, ok := client.WrongState(err)
	require.True(t, ok)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_SNAPSHOT, wrongState.Found)
}

func TestCreateSnapshotCreates(t *testing.T) {
	fake := &fakeRunner{outputs: []string{noLVs, readyPushLV}}
	volumes, _ := testServer(t, fake)

	_, err := volumes.CreateSnapshot(t.Context(), &beanstorev1.CreateSnapshotRequest{
		VolumeId: "vol-1", SnapshotId: "snap-1",
	})

	require.NoError(t, err)
	commands := allCommands(fake)
	created := commands[len(commands)-1]
	assert.Contains(t, created, "lvcreate -s")
	assert.Contains(t, created, "-n snap-1")
	assert.Contains(t, created, "vg0/vol-1")
}

func TestDeleteSnapshotRefusesVolumes(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{readyPushLV}})

	_, err := volumes.DeleteSnapshot(t.Context(), &beanstorev1.DeleteSnapshotRequest{SnapshotId: "vol-1"})

	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestDeleteSnapshotUnknown(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{noLVs}})

	_, err := volumes.DeleteSnapshot(t.Context(), &beanstorev1.DeleteSnapshotRequest{SnapshotId: "snap-9"})

	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestDeleteSnapshotRefusesLiveExport(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{snapshotLV("")}})
	volumes.pins.Acquire("snap-1")

	_, err := volumes.DeleteSnapshot(t.Context(), &beanstorev1.DeleteSnapshotRequest{SnapshotId: "snap-1"})

	assert.Equal(t, codes.Aborted, status.Code(err))
}

func TestDeleteSnapshotRemoves(t *testing.T) {
	fake := &fakeRunner{outputs: []string{snapshotLV("")}}
	volumes, _ := testServer(t, fake)

	_, err := volumes.DeleteSnapshot(t.Context(), &beanstorev1.DeleteSnapshotRequest{SnapshotId: "snap-1"})

	require.NoError(t, err)
	assert.Contains(t, allCommands(fake), "lvremove -f vg0/snap-1")
}

// serveVolumes exposes the real server over bufconn, streaming verbs
// need a live stream.
func serveVolumes(t *testing.T, volumes *volumeServiceServer) beanstorev1.VolumeServiceClient {
	t.Helper()

	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	beanstorev1.RegisterVolumeServiceServer(server, volumes)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///volumes",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return beanstorev1.NewVolumeServiceClient(conn)
}

func TestExportUnknownSnapshot(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{noLVs}})
	remote := serveVolumes(t, volumes)

	stream, err := remote.Export(t.Context(), &beanstorev1.ExportRequest{SnapshotId: "snap-9"})
	require.NoError(t, err)
	_, err = stream.Recv()

	assert.Equal(t, codes.NotFound, status.Code(err))
	assert.False(t, volumes.pins.Pinned("snap-9"), "the failed export released its pin")
}

func TestExportRefusesVolumes(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{readyPushLV}})
	remote := serveVolumes(t, volumes)

	stream, err := remote.Export(t.Context(), &beanstorev1.ExportRequest{SnapshotId: "vol-1"})
	require.NoError(t, err)
	_, err = stream.Recv()

	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.False(t, volumes.pins.Pinned("vol-1"))
}

func TestExportStreamsAndReleases(t *testing.T) {
	content := make([]byte, 4096)
	for i := range content {
		content[i] = byte(i%254) + 1
	}
	path := filepath.Join(t.TempDir(), "snapshot")
	require.NoError(t, os.WriteFile(path, content, 0o600))

	fake := &fakeRunner{outputs: []string{snapshotLV(path), snapshotLV(path)}}
	volumes, _ := testServer(t, fake)
	remote := serveVolumes(t, volumes)

	stream, err := remote.Export(t.Context(), &beanstorev1.ExportRequest{SnapshotId: "snap-1"})
	require.NoError(t, err)

	var frames []*beanstorev1.Frame
	var trailer *beanstorev1.ExportTrailer
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if trailer = response.GetTrailer(); trailer == nil {
			frames = append(frames, response.GetFrame())
		}
	}

	require.Len(t, frames, 1)
	assert.Equal(t, content, frames[0].Data)

	expected := storage.NewDigestBuilder()
	expected.AddChunk(content)
	require.NotNil(t, trailer)
	assert.Equal(t, expected.Sum(uint64(len(content))), trailer.Digest)

	assert.False(t, volumes.pins.Pinned("snap-1"), "the finished export released its pin")
	commands := allCommands(fake)
	deactivated := commands[len(commands)-1]
	assert.Contains(t, deactivated, "-a n")
	assert.Contains(t, deactivated, "vg0/snap-1")
}
