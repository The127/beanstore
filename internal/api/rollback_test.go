package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
)

func rollbackRequest() *beanstorev1.RollbackVolumeRequest {
	return &beanstorev1.RollbackVolumeRequest{
		VolumeId:         "vol-1",
		SourceSnapshotId: "snap-1",
	}
}

// tempLV renders the rollback copy for vol-1 under the given name.
func tempLV(name string) string {
	return fmt.Sprintf(`{"report": [{"lv": [
	{"lv_name": %q, "lv_uuid": "uuid-t", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=rollback,beanstore.rollback_target=vol-1",
	 "pool_lv": "pool0", "origin": "snap-1", "lv_path": "", "lv_dm_path": "",
	 "data_percent": "", "metadata_percent": "", "lv_active": "",
	 "lv_layout": "thin,sparse"}
]}], "log": []}`, name)
}

func TestRollbackVolumeReplacesTheVolume(t *testing.T) {
	fake := &fakeRunner{outputs: []string{
		noLVs,              // sweep: no leftover temp
		readyPushLV,        // sweep: the volume is not a stranded copy
		readyPushLV,        // begin: the volume is READY
		snapshotLV(""),     // begin: the snapshot with lineage vol-1
		tempLV("vol-1+rb"), // finish: the temp still holds its name
	}}
	volumes, _ := testServer(t, fake)

	_, err := volumes.RollbackVolume(t.Context(), rollbackRequest())

	require.NoError(t, err)
	commands := strings.Join(allCommands(fake), "\n")
	assert.Contains(t, commands, "lvcreate -s -n vol-1+rb")
	assert.Contains(t, commands, "lvremove -f vg0/vol-1")
	assert.Contains(t, commands, "lvrename vg0 vol-1+rb vol-1")
	assert.Contains(t, commands, "--addtag beanstore.state=ready")
	assert.Contains(t, commands, "-k n")
	assert.False(t, volumes.reserved.Reserved("vol-1", "vol-1+rb"), "the reservation lifted")
}

func TestRollbackVolumeRefusesWrongLineage(t *testing.T) {
	fake := &fakeRunner{outputs: []string{noLVs, readyPushLV, readyPushLV, foreignOriginSnapLV}}
	volumes, _ := testServer(t, fake)

	_, err := volumes.RollbackVolume(t.Context(), rollbackRequest())

	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.ErrorContains(t, err, "does not belong")
}

const foreignOriginSnapLV = `{"report": [{"lv": [
	{"lv_name": "snap-1", "lv_uuid": "uuid-s", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vri---tz--",
	 "lv_tags": "beanstore.state=snapshot,beanstore.origin=vol-other",
	 "pool_lv": "pool0", "origin": "", "lv_path": "", "lv_dm_path": "",
	 "data_percent": "", "metadata_percent": "", "lv_active": "",
	 "lv_layout": "thin,sparse"}
]}], "log": []}`

func TestRollbackVolumeSweepsLeftovers(t *testing.T) {
	// a leftover temp whose target is gone finishes before the verb
	// proceeds, the missing snapshot then refuses the new rollback
	fake := &fakeRunner{outputs: []string{
		tempLV("vol-1+rb"), // sweep: the leftover
		noLVs,              // sweep: the target is gone
		tempLV("vol-1+rb"), // finish: the temp holds its name
		readyPushLV,        // renamed sweep: the volume is settled
		readyPushLV,        // begin: the volume is READY
		noLVs,              // begin: the snapshot is gone
	}}
	volumes, _ := testServer(t, fake)

	_, err := volumes.RollbackVolume(t.Context(), rollbackRequest())

	assert.Equal(t, codes.NotFound, status.Code(err))
	commands := strings.Join(allCommands(fake), "\n")
	assert.Contains(t, commands, "lvrename vg0 vol-1+rb vol-1", "the leftover finished")
}

func TestRollbackReservationBlocksCreators(t *testing.T) {
	fake := &fakeRunner{outputs: []string{noLVs}}
	volumes, _ := testServer(t, fake)
	require.True(t, volumes.reserved.Reserve("vol-1"))

	_, err := volumes.CreateVolume(t.Context(), &beanstorev1.CreateVolumeRequest{
		VolumeId: "vol-1", SizeBytes: 1 << 20, OperationId: "op-1",
	})
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	_, err = volumes.PrepareReceive(t.Context(), &beanstorev1.PrepareReceiveRequest{
		VolumeId: "vol-1", SizeBytes: 1 << 20, TransferId: "tr-1",
	})
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestRollbackVolumeValidation(t *testing.T) {
	volumes, _ := testServer(t, &fakeRunner{})

	_, err := volumes.RollbackVolume(t.Context(), &beanstorev1.RollbackVolumeRequest{
		VolumeId: "-bad", SourceSnapshotId: "snap-1",
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = volumes.RollbackVolume(t.Context(), &beanstorev1.RollbackVolumeRequest{
		VolumeId: "vol-1", SourceSnapshotId: "",
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
