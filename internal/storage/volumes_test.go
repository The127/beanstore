package storage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/The127/beanstore/lvm"
)

const scanLVs = `{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi-a-tz--",
	 "lv_tags": "beanstore.state=ready", "pool_lv": "pool0", "origin": "",
	 "lv_path": "/dev/vg0/vol-1", "lv_dm_path": "",
	 "data_percent": "25.00", "metadata_percent": "",
	 "lv_active": "active", "lv_layout": "thin,sparse"},
	{"lv_name": "foreign", "lv_uuid": "uuid-2", "vg_name": "vg0",
	 "lv_size": "536870912", "lv_attr": "Vwi-a-tz--", "lv_tags": "backup",
	 "pool_lv": "pool0", "origin": "", "lv_path": "", "lv_dm_path": "",
	 "data_percent": "0.00", "metadata_percent": "",
	 "lv_active": "active", "lv_layout": "thin,sparse"},
	{"lv_name": "vol-2", "lv_uuid": "uuid-3", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=exploded", "pool_lv": "pool0", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "",
	 "metadata_percent": "", "lv_active": "", "lv_layout": "thin,sparse"}
]}], "log": []}`

func TestListVolumesScansTaggedLVs(t *testing.T) {
	fake := &fakeRunner{outputs: []string{scanLVs}}
	client := lvm.New(lvm.WithRunner(fake))

	volumes, err := ListVolumes(t.Context(), client, testConfig())

	require.NoError(t, err)
	require.Len(t, fake.calls, 1)
	assert.Contains(t, fake.calls[0].Args(), "pool_lv = pool0")

	require.Len(t, volumes, 2, "the untagged lv is foreign")
	assert.Equal(t, Volume{
		ID:        "vol-1",
		State:     StateReady,
		SizeBytes: 1 << 30,
		UsedBytes: 1 << 28,
		Path:      "/dev/vg0/vol-1",
	}, volumes[0])
	assert.Equal(t, StateUnknown, volumes[1].State, "broken state tags are reported, not hidden")
}

const readyLV = `{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=ready", "pool_lv": "pool0", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "",
	 "metadata_percent": "", "lv_active": "", "lv_layout": "thin,sparse"}
]}], "log": []}`

const attachedLV = `{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi-a-tz--",
	 "lv_tags": "beanstore.state=attached", "pool_lv": "pool0", "origin": "",
	 "lv_path": "/dev/vg0/vol-1", "lv_dm_path": "", "data_percent": "1.00",
	 "metadata_percent": "", "lv_active": "active",
	 "lv_layout": "thin,sparse"}
]}], "log": []}`

func TestAttachActivatesAndReturnsPath(t *testing.T) {
	fake := &fakeRunner{outputs: []string{readyLV, "", "", attachedLV}}
	client := lvm.New(lvm.WithRunner(fake))

	path, err := Attach(t.Context(), client, testConfig(), "vol-1")

	require.NoError(t, err)
	assert.Equal(t, "/dev/vg0/vol-1", path)

	retagged := strings.Join(fake.calls[1].Args(), " ")
	assert.Contains(t, retagged, "--addtag beanstore.state=attached")
	assert.Contains(t, retagged, "--deltag beanstore.state=ready")
	assert.Contains(t, strings.Join(fake.calls[2].Args(), " "), "-a y")
}

func TestAttachRefusesWrongState(t *testing.T) {
	fake := &fakeRunner{outputs: []string{attachedLV}}
	client := lvm.New(lvm.WithRunner(fake))

	_, err := Attach(t.Context(), client, testConfig(), "vol-1")

	var wrongState *WrongStateError
	require.ErrorAs(t, err, &wrongState)
	assert.Equal(t, StateAttached, wrongState.Found)
	assert.Len(t, fake.calls, 1, "no tag or activation commands ran")
}

func TestAttachUnknownVolume(t *testing.T) {
	fake := &fakeRunner{outputs: []string{noLVs}}
	client := lvm.New(lvm.WithRunner(fake))

	_, err := Attach(t.Context(), client, testConfig(), "vol-9")

	assert.ErrorIs(t, err, ErrNotFound)
}

func TestDetachDeactivatesAndRetags(t *testing.T) {
	fake := &fakeRunner{outputs: []string{attachedLV, "", ""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := Detach(t.Context(), client, testConfig(), "vol-1")

	require.NoError(t, err)
	assert.Contains(t, strings.Join(fake.calls[1].Args(), " "), "-a n")

	retagged := strings.Join(fake.calls[2].Args(), " ")
	assert.Contains(t, retagged, "--addtag beanstore.state=ready")
	assert.Contains(t, retagged, "--deltag beanstore.state=attached")
}

func TestDetachRefusesWrongState(t *testing.T) {
	fake := &fakeRunner{outputs: []string{readyLV}}
	client := lvm.New(lvm.WithRunner(fake))

	err := Detach(t.Context(), client, testConfig(), "vol-1")

	var wrongState *WrongStateError
	require.ErrorAs(t, err, &wrongState)
	assert.Equal(t, StateReady, wrongState.Found)
	assert.Len(t, fake.calls, 1)
}

func TestMarkDeletingRetags(t *testing.T) {
	fake := &fakeRunner{outputs: []string{readyLV, ""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := MarkDeleting(t.Context(), client, testConfig(), "vol-1")

	require.NoError(t, err)
	retagged := strings.Join(fake.calls[1].Args(), " ")
	assert.Contains(t, retagged, "--addtag beanstore.state=deleting")
	assert.Contains(t, retagged, "--deltag beanstore.state=ready")
}

func TestMarkDeletingRefusesAttached(t *testing.T) {
	fake := &fakeRunner{outputs: []string{attachedLV}}
	client := lvm.New(lvm.WithRunner(fake))

	err := MarkDeleting(t.Context(), client, testConfig(), "vol-1")

	var wrongState *WrongStateError
	require.ErrorAs(t, err, &wrongState)
	assert.Equal(t, StateAttached, wrongState.Found)
	assert.Len(t, fake.calls, 1)
}

func TestRemoveVolumeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{outputs: []string{""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := RemoveVolume(t.Context(), client, testConfig(), "vol-1")

	require.NoError(t, err)
	assert.Equal(t, []string{"lvremove", "-f", "vg0/vol-1"}, fake.calls[0].Args())
}

func TestStateTagRoundTrip(t *testing.T) {
	state, owned := stateOf([]string{"other", StateTag(StateAttached)})

	assert.True(t, owned)
	assert.Equal(t, StateAttached, state)
}
