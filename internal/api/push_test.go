package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/operations"
	"github.com/The127/beanstore/internal/storage"
)

// fakeTarget scripts the destination node of a push.
type fakeTarget struct {
	beanstorev1.UnimplementedTransferServiceServer
	beanstorev1.UnimplementedVolumeServiceServer

	mu          sync.Mutex
	dropStreams int
	commitCodes []codes.Code
	listed      []*beanstorev1.Volume

	nextOffset   uint64
	streams      int
	frames       []*beanstorev1.Frame
	commitDigest []byte
	aborts       int
}

func (f *fakeTarget) QueryTransfer(_ context.Context, _ *beanstorev1.QueryTransferRequest) (*beanstorev1.QueryTransferResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return &beanstorev1.QueryTransferResponse{NextOffset: f.nextOffset}, nil
}

func (f *fakeTarget) Receive(stream grpc.ClientStreamingServer[beanstorev1.ReceiveRequest, beanstorev1.ReceiveResponse]) error {
	first, err := stream.Recv()
	if err != nil || first.GetHeader() == nil {
		return status.Error(codes.InvalidArgument, "missing header")
	}

	f.mu.Lock()
	f.streams++
	drop := f.dropStreams > 0
	if drop {
		f.dropStreams--
	}
	f.mu.Unlock()
	if drop {
		return status.Error(codes.Unavailable, "stream dropped")
	}

	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&beanstorev1.ReceiveResponse{})
		}
		if err != nil {
			return err
		}

		frame := request.GetFrame()
		f.mu.Lock()
		f.frames = append(f.frames, frame)
		f.nextOffset = frame.Offset + uint64(len(frame.Data))
		f.mu.Unlock()
	}
}

func (f *fakeTarget) CommitTransfer(_ context.Context, request *beanstorev1.CommitTransferRequest) (*beanstorev1.CommitTransferResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.commitCodes) > 0 {
		code := f.commitCodes[0]
		f.commitCodes = f.commitCodes[1:]

		return nil, status.Error(code, "scripted commit refusal")
	}

	f.commitDigest = request.Digest

	return &beanstorev1.CommitTransferResponse{}, nil
}

func (f *fakeTarget) AbortTransfer(_ context.Context, _ *beanstorev1.AbortTransferRequest) (*beanstorev1.AbortTransferResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.aborts++

	return &beanstorev1.AbortTransferResponse{}, nil
}

func (f *fakeTarget) ListVolumes(_ context.Context, _ *beanstorev1.ListVolumesRequest) (*beanstorev1.ListVolumesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return &beanstorev1.ListVolumesResponse{Volumes: f.listed}, nil
}

func pushHarness(t *testing.T, fake *fakeRunner, target *fakeTarget) *volumeServiceServer {
	t.Helper()

	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	beanstorev1.RegisterTransferServiceServer(server, target)
	beanstorev1.RegisterVolumeServiceServer(server, target)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	volumes, _ := testServer(t, fake)
	volumes.pushRetryDelay = 0
	volumes.dial = func(string) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///target",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return listener.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	return volumes
}

func pushDevice(t *testing.T, content []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "device")
	require.NoError(t, os.WriteFile(path, content, 0o600))

	return path
}

const readyPushLV = `{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=ready", "pool_lv": "pool0", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "",
	 "metadata_percent": "", "lv_active": "", "lv_layout": "thin,sparse"}
]}], "log": []}`

// pushLV renders vol-1 in a push state with its device at path.
func pushLV(state, path string) string {
	return fmt.Sprintf(`{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi-a-tz--",
	 "lv_tags": "beanstore.state=%s,beanstore.transfer=tr-1,beanstore.target=127.0.0.1:19999",
	 "pool_lv": "pool0", "origin": "", "lv_path": %q, "lv_dm_path": "",
	 "data_percent": "1.00", "metadata_percent": "", "lv_active": "active",
	 "lv_layout": "thin,sparse"}
]}], "log": []}`, state, path)
}

func pushRequest() *beanstorev1.PushVolumeRequest {
	return &beanstorev1.PushVolumeRequest{
		VolumeId:      "vol-1",
		TransferId:    "tr-1",
		TargetAddress: "127.0.0.1:19999",
		OperationId:   "op-1",
	}
}

func awaitPhase(t *testing.T, volumes *volumeServiceServer, phase operations.Phase) {
	t.Helper()

	assert.Eventually(t, func() bool {
		op, _ := volumes.ops.Get("op-1")
		return op.Phase == phase
	}, 5*time.Second, 10*time.Millisecond)
}

func allCommands(fake *fakeRunner) []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()

	lines := make([]string, len(fake.calls))
	for i, call := range fake.calls {
		lines[i] = strings.Join(call.Args(), " ")
	}

	return lines
}

func lastRetag(fake *fakeRunner) string {
	commands := allCommands(fake)
	for i := len(commands) - 1; i >= 0; i-- {
		if strings.Contains(commands[i], "--addtag") {
			return commands[i]
		}
	}

	return ""
}

func TestPushVolumeRunsToRetired(t *testing.T) {
	content := bytes.Repeat([]byte{5}, 4096)
	path := pushDevice(t, content)
	target := &fakeTarget{}
	fake := &fakeRunner{outputs: []string{
		readyPushLV, pushLV("pushing", path), pushLV("pushing", path), pushLV("committing", path),
	}}
	volumes := pushHarness(t, fake, target)

	_, err := volumes.PushVolume(t.Context(), pushRequest())

	require.NoError(t, err)
	awaitPhase(t, volumes, operations.Done)

	require.Len(t, target.frames, 1)
	assert.Equal(t, uint64(0), target.frames[0].Offset)
	assert.Equal(t, content, target.frames[0].Data)

	expected := storage.NewDigestBuilder()
	expected.AddChunk(content)
	assert.Equal(t, expected.Sum(uint64(len(content))), target.commitDigest)

	retagged := lastRetag(fake)
	assert.Contains(t, retagged, "--addtag beanstore.state=retired")
	assert.Contains(t, retagged, "--deltag beanstore.state=committing")
}

func TestPushVolumeResumesAfterStreamDrop(t *testing.T) {
	content := bytes.Repeat([]byte{6}, 512)
	path := pushDevice(t, content)
	target := &fakeTarget{dropStreams: 1}
	fake := &fakeRunner{outputs: []string{
		readyPushLV, pushLV("pushing", path), pushLV("pushing", path), pushLV("committing", path),
	}}
	volumes := pushHarness(t, fake, target)

	_, err := volumes.PushVolume(t.Context(), pushRequest())

	require.NoError(t, err)
	awaitPhase(t, volumes, operations.Done)

	assert.Equal(t, 2, target.streams, "the second stream resumed the push")
	require.Len(t, target.frames, 1)
	assert.Equal(t, content, target.frames[0].Data)
}

func TestPushVolumeAbortsOnDigestMismatch(t *testing.T) {
	path := pushDevice(t, []byte{1})
	target := &fakeTarget{commitCodes: []codes.Code{codes.DataLoss}}
	fake := &fakeRunner{outputs: []string{
		readyPushLV, pushLV("pushing", path), pushLV("pushing", path), pushLV("committing", path),
	}}
	volumes := pushHarness(t, fake, target)

	_, err := volumes.PushVolume(t.Context(), pushRequest())

	require.NoError(t, err)
	awaitPhase(t, volumes, operations.Failed)

	retagged := lastRetag(fake)
	assert.Contains(t, retagged, "--addtag beanstore.state=ready")
	assert.Contains(t, retagged, "--deltag beanstore.state=committing")
}

func TestPushVolumeResolvesLostCommitByTargetState(t *testing.T) {
	path := pushDevice(t, []byte{2})
	target := &fakeTarget{
		commitCodes: []codes.Code{
			codes.Unavailable, codes.Unavailable, codes.Unavailable,
			codes.Unavailable, codes.Unavailable,
		},
		listed: []*beanstorev1.Volume{{
			VolumeId: "vol-1", State: beanstorev1.VolumeState_VOLUME_STATE_READY,
		}},
	}
	fake := &fakeRunner{outputs: []string{
		readyPushLV, pushLV("pushing", path), pushLV("pushing", path), pushLV("committing", path),
	}}
	volumes := pushHarness(t, fake, target)

	_, err := volumes.PushVolume(t.Context(), pushRequest())

	require.NoError(t, err)
	awaitPhase(t, volumes, operations.Done)

	retagged := lastRetag(fake)
	assert.Contains(t, retagged, "--addtag beanstore.state=retired")
}

func TestPushVolumeRevertsWhenTargetNeverCommitted(t *testing.T) {
	path := pushDevice(t, []byte{3})
	target := &fakeTarget{commitCodes: []codes.Code{codes.NotFound}}
	fake := &fakeRunner{outputs: []string{
		readyPushLV, pushLV("pushing", path), pushLV("pushing", path), pushLV("committing", path),
	}}
	volumes := pushHarness(t, fake, target)

	_, err := volumes.PushVolume(t.Context(), pushRequest())

	require.NoError(t, err)
	awaitPhase(t, volumes, operations.Failed)

	assert.Equal(t, 1, target.aborts, "the lingering transfer was aborted")
	retagged := lastRetag(fake)
	assert.Contains(t, retagged, "--addtag beanstore.state=ready")
	assert.Contains(t, retagged, "--deltag beanstore.transfer=tr-1")
}

func TestPushVolumeRefusesWrongState(t *testing.T) {
	fake := &fakeRunner{outputs: []string{pushLV("pushing", "")}}
	volumes, _ := testServer(t, fake)

	_, err := volumes.PushVolume(t.Context(), pushRequest())

	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	op, _ := volumes.ops.Get("op-1")
	assert.Equal(t, operations.Failed, op.Phase)
}

func TestPushVolumeValidation(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{})

	broken := map[string]func(*beanstorev1.PushVolumeRequest){
		"volume":    func(r *beanstorev1.PushVolumeRequest) { r.VolumeId = "-bad" },
		"transfer":  func(r *beanstorev1.PushVolumeRequest) { r.TransferId = "" },
		"target":    func(r *beanstorev1.PushVolumeRequest) { r.TargetAddress = "no port" },
		"operation": func(r *beanstorev1.PushVolumeRequest) { r.OperationId = "" },
	}
	for name, mutate := range broken {
		request := pushRequest()
		mutate(request)

		_, err := volumes.PushVolume(t.Context(), request)

		assert.Equal(t, codes.InvalidArgument, status.Code(err), name)
	}
}
