package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/The127/beanstore/client"
	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/operations"
)

func copyRequest() *beanstorev1.CreateVolumeFromSnapshotRequest {
	return &beanstorev1.CreateVolumeFromSnapshotRequest{
		VolumeId:         "vol-1",
		SourceSnapshotId: "snap-1",
		OperationId:      "op-1",
	}
}

func TestCreateVolumeFromSnapshotCopies(t *testing.T) {
	content := make([]byte, 4096)
	for i := range content {
		content[i] = byte(i%253) + 1
	}
	source := filepath.Join(t.TempDir(), "snapshot")
	require.NoError(t, os.WriteFile(source, content, 0o600))
	target := filepath.Join(t.TempDir(), "volume")
	require.NoError(t, os.WriteFile(target, nil, 0o600))

	fake := &fakeRunner{outputs: []string{
		snapshotLV(source), noLVs, pushLV("creating", target), snapshotLV(source),
	}}
	volumes, _ := testServer(t, fake)

	_, err := volumes.CreateVolumeFromSnapshot(t.Context(), copyRequest())

	require.NoError(t, err)
	awaitPhase(t, volumes, operations.Done)

	copied, err := os.ReadFile(target) //nolint:gosec // test file
	require.NoError(t, err)
	assert.Equal(t, content, copied)

	commands := strings.Join(allCommands(fake), "\n")
	assert.Contains(t, commands, "--addtag beanstore.state=creating")
	assert.Contains(t, commands, "--addtag beanstore.state=ready")
	assert.Contains(t, commands, "--deltag beanstore.state=creating")
	assert.Contains(t, commands, "-K", "the snapshot activates past the skip flag")
	assert.False(t, volumes.pins.Pinned("snap-1"), "the finished copy released its pin")
}

func TestCreateVolumeFromSnapshotRefusesWrongState(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{readyPushLV}})

	request := copyRequest()
	request.SourceSnapshotId = "vol-1"
	_, err := volumes.CreateVolumeFromSnapshot(t.Context(), request)

	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	wrongState, ok := client.WrongState(err)
	require.True(t, ok)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_READY, wrongState.Found)
	assert.False(t, volumes.pins.Pinned("vol-1"), "the refusal released its pin")
	op, _ := volumes.ops.Get("op-1")
	assert.Equal(t, operations.Failed, op.Phase)
}

func TestCreateVolumeFromSnapshotRefusesTakenName(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{snapshotLV(""), readyPushLV}})

	_, err := volumes.CreateVolumeFromSnapshot(t.Context(), copyRequest())

	assert.Equal(t, codes.AlreadyExists, status.Code(err))
	assert.False(t, volumes.pins.Pinned("snap-1"))
}

func TestCreateVolumeFromSnapshotRemovesFailedCopy(t *testing.T) {
	target := filepath.Join(t.TempDir(), "volume")
	require.NoError(t, os.WriteFile(target, nil, 0o600))

	// the source device path does not exist, the copy fails after the
	// target was created
	fake := &fakeRunner{outputs: []string{
		snapshotLV("/nonexistent/snapshot"), noLVs, pushLV("creating", target), snapshotLV("/nonexistent/snapshot"),
	}}
	volumes, _ := testServer(t, fake)

	_, err := volumes.CreateVolumeFromSnapshot(t.Context(), copyRequest())

	require.NoError(t, err)
	awaitPhase(t, volumes, operations.Failed)

	assert.Contains(t, allCommands(fake), "lvremove -f vg0/vol-1", "the half-made target is removed")
	assert.False(t, volumes.pins.Pinned("snap-1"))
}

func TestCreateVolumeFromSnapshotValidation(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{})

	broken := map[string]func(*beanstorev1.CreateVolumeFromSnapshotRequest){
		"volume":    func(r *beanstorev1.CreateVolumeFromSnapshotRequest) { r.VolumeId = "-bad" },
		"snapshot":  func(r *beanstorev1.CreateVolumeFromSnapshotRequest) { r.SourceSnapshotId = "" },
		"operation": func(r *beanstorev1.CreateVolumeFromSnapshotRequest) { r.OperationId = "" },
	}
	for name, mutate := range broken {
		request := copyRequest()
		mutate(request)

		_, err := volumes.CreateVolumeFromSnapshot(t.Context(), request)

		assert.Equal(t, codes.InvalidArgument, status.Code(err), name)
	}

	require.NoError(t, volumes.ops.Begin("op-taken"))
	request := copyRequest()
	request.OperationId = "op-taken"
	_, err := volumes.CreateVolumeFromSnapshot(t.Context(), request)
	assert.Equal(t, codes.AlreadyExists, status.Code(err), "operation ids are single use")
}
