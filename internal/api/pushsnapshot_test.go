package api

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/The127/beanstore/client"
	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/operations"
	"github.com/The127/beanstore/internal/storage"
)

func pushSnapshotRequest() *beanstorev1.PushSnapshotRequest {
	return &beanstorev1.PushSnapshotRequest{
		SnapshotId:    "snap-1",
		TransferId:    "tr-1",
		TargetAddress: "127.0.0.1:19999",
		OperationId:   "op-1",
	}
}

func TestPushSnapshotRunsToDone(t *testing.T) {
	content := bytes.Repeat([]byte{8}, 4096)
	path := pushDevice(t, content)
	target := &fakeTarget{}
	fake := &fakeRunner{outputs: []string{snapshotLV(path), snapshotLV(path)}}
	volumes := pushHarness(t, fake, target)

	_, err := volumes.PushSnapshot(t.Context(), pushSnapshotRequest())

	require.NoError(t, err)
	awaitPhase(t, volumes, operations.Done)

	require.Len(t, target.frames, 1)
	assert.Equal(t, content, target.frames[0].Data)

	expected := storage.NewDigestBuilder()
	expected.AddChunk(content)
	assert.Equal(t, expected.Sum(uint64(len(content))), target.commitDigest)

	assert.False(t, volumes.pins.Pinned("snap-1"), "the finished push released its pin")
	for _, command := range allCommands(fake) {
		assert.NotContains(t, command, "--addtag", "a copy changes no source state")
	}
	assert.Contains(t, strings.Join(allCommands(fake), "\n"), "-K", "snapshots activate past the skip flag")
}

func TestPushSnapshotRefusesVolumes(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{readyPushLV}})

	request := pushSnapshotRequest()
	request.SnapshotId = "vol-1"
	_, err := volumes.PushSnapshot(t.Context(), request)

	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	wrongState, ok := client.WrongState(err)
	require.True(t, ok)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_READY, wrongState.Found)
	assert.False(t, volumes.pins.Pinned("vol-1"), "the refusal released its pin")
	op, _ := volumes.ops.Get("op-1")
	assert.Equal(t, operations.Failed, op.Phase)
}

func TestPushSnapshotFailsOnRefusedCommit(t *testing.T) {
	path := pushDevice(t, []byte{4})
	target := &fakeTarget{commitCodes: []codes.Code{codes.DataLoss}}
	fake := &fakeRunner{outputs: []string{snapshotLV(path), snapshotLV(path)}}
	volumes := pushHarness(t, fake, target)

	_, err := volumes.PushSnapshot(t.Context(), pushSnapshotRequest())

	require.NoError(t, err)
	awaitPhase(t, volumes, operations.Failed)

	assert.False(t, volumes.pins.Pinned("snap-1"))
	for _, command := range allCommands(fake) {
		assert.NotContains(t, command, "--addtag", "the failed copy changed no source state")
	}
}

func TestPushSnapshotUnknownCommitAborts(t *testing.T) {
	path := pushDevice(t, []byte{5})
	target := &fakeTarget{commitCodes: []codes.Code{
		codes.Unavailable, codes.Unavailable, codes.Unavailable,
		codes.Unavailable, codes.Unavailable,
	}}
	fake := &fakeRunner{outputs: []string{snapshotLV(path), snapshotLV(path)}}
	volumes := pushHarness(t, fake, target)

	_, err := volumes.PushSnapshot(t.Context(), pushSnapshotRequest())

	require.NoError(t, err)
	awaitPhase(t, volumes, operations.Failed)

	op, _ := volumes.ops.Get("op-1")
	assert.Contains(t, op.Reason, "commit outcome unknown")
	assert.Equal(t, 1, target.abortCount(), "the lingering transfer was aborted")
	assert.False(t, volumes.pins.Pinned("snap-1"))
}

func TestPushSnapshotValidation(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{})

	broken := map[string]func(*beanstorev1.PushSnapshotRequest){
		"snapshot":  func(r *beanstorev1.PushSnapshotRequest) { r.SnapshotId = "-bad" },
		"transfer":  func(r *beanstorev1.PushSnapshotRequest) { r.TransferId = "" },
		"target":    func(r *beanstorev1.PushSnapshotRequest) { r.TargetAddress = "no port" },
		"operation": func(r *beanstorev1.PushSnapshotRequest) { r.OperationId = "" },
	}
	for name, mutate := range broken {
		request := pushSnapshotRequest()
		mutate(request)

		_, err := volumes.PushSnapshot(t.Context(), request)

		assert.Equal(t, codes.InvalidArgument, status.Code(err), name)
	}
}
