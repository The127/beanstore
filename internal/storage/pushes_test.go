package storage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/The127/beanstore/lvm"
)

const pushingLV = `{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi-a-tz--",
	 "lv_tags": "beanstore.state=pushing,beanstore.transfer=tr-1,beanstore.target=10.0.0.9:50051",
	 "pool_lv": "pool0", "origin": "", "lv_path": "/dev/vg0/vol-1",
	 "lv_dm_path": "", "data_percent": "1.00", "metadata_percent": "",
	 "lv_active": "active", "lv_layout": "thin,sparse"}
]}], "log": []}`

const committingLV = `{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi-a-tz--",
	 "lv_tags": "beanstore.state=committing,beanstore.transfer=tr-1,beanstore.target=10.0.0.9:50051",
	 "pool_lv": "pool0", "origin": "", "lv_path": "/dev/vg0/vol-1",
	 "lv_dm_path": "", "data_percent": "1.00", "metadata_percent": "",
	 "lv_active": "active", "lv_layout": "thin,sparse"}
]}], "log": []}`

const committingIdleLV = `{"report": [{"lv": [
	{"lv_name": "vol-1", "lv_uuid": "uuid-1", "vg_name": "vg0",
	 "lv_size": "1073741824", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=committing,beanstore.transfer=tr-1,beanstore.target=10.0.0.9:50051",
	 "pool_lv": "pool0", "origin": "", "lv_path": "", "lv_dm_path": "",
	 "data_percent": "", "metadata_percent": "", "lv_active": "",
	 "lv_layout": "thin,sparse"}
]}], "log": []}`

func TestMarkPushingRetagsAndPersistsTarget(t *testing.T) {
	fake := &fakeRunner{outputs: []string{readyLV, ""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := MarkPushing(t.Context(), client, testConfig(), "vol-1", "tr-1", "10.0.0.9:50051")

	require.NoError(t, err)
	retagged := strings.Join(fake.calls[1].Args(), " ")
	assert.Contains(t, retagged, "--addtag beanstore.state=pushing")
	assert.Contains(t, retagged, "--addtag beanstore.transfer=tr-1")
	assert.Contains(t, retagged, "--addtag beanstore.target=10.0.0.9:50051")
	assert.Contains(t, retagged, "--deltag beanstore.state=ready")
}

func TestMarkPushingRefusesWrongState(t *testing.T) {
	fake := &fakeRunner{outputs: []string{attachedLV}}
	client := lvm.New(lvm.WithRunner(fake))

	err := MarkPushing(t.Context(), client, testConfig(), "vol-1", "tr-1", "10.0.0.9:50051")

	var wrongState *WrongStateError
	require.ErrorAs(t, err, &wrongState)
	assert.Equal(t, StateAttached, wrongState.Found)
	assert.Len(t, fake.calls, 1, "no tag commands ran")
}

func TestMarkCommittingRetags(t *testing.T) {
	fake := &fakeRunner{outputs: []string{pushingLV, ""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := MarkCommitting(t.Context(), client, testConfig(), "vol-1")

	require.NoError(t, err)
	retagged := strings.Join(fake.calls[1].Args(), " ")
	assert.Contains(t, retagged, "--addtag beanstore.state=committing")
	assert.Contains(t, retagged, "--deltag beanstore.state=pushing")
	assert.NotContains(t, retagged, "beanstore.transfer", "the push tags stay for resolution")
}

func TestMarkCommittingRefusesWrongState(t *testing.T) {
	fake := &fakeRunner{outputs: []string{readyLV}}
	client := lvm.New(lvm.WithRunner(fake))

	err := MarkCommitting(t.Context(), client, testConfig(), "vol-1")

	var wrongState *WrongStateError
	require.ErrorAs(t, err, &wrongState)
	assert.Equal(t, StateReady, wrongState.Found)
}

func TestRetirePushedDeactivatesAndDropsPushTags(t *testing.T) {
	fake := &fakeRunner{outputs: []string{committingLV, "", ""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := RetirePushed(t.Context(), client, testConfig(), "vol-1")

	require.NoError(t, err)
	assert.Contains(t, strings.Join(fake.calls[1].Args(), " "), "-a n")

	retagged := strings.Join(fake.calls[2].Args(), " ")
	assert.Contains(t, retagged, "--addtag beanstore.state=retired")
	assert.Contains(t, retagged, "--deltag beanstore.state=committing")
	assert.Contains(t, retagged, "--deltag beanstore.transfer=tr-1")
	assert.Contains(t, retagged, "--deltag beanstore.target=10.0.0.9:50051")
}

func TestRetirePushedRefusesWrongState(t *testing.T) {
	fake := &fakeRunner{outputs: []string{pushingLV}}
	client := lvm.New(lvm.WithRunner(fake))

	err := RetirePushed(t.Context(), client, testConfig(), "vol-1")

	var wrongState *WrongStateError
	require.ErrorAs(t, err, &wrongState)
	assert.Equal(t, StatePushing, wrongState.Found)
}

func TestAbortPushRevertsPushing(t *testing.T) {
	fake := &fakeRunner{outputs: []string{pushingLV, "", ""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := AbortPush(t.Context(), client, testConfig(), "vol-1")

	require.NoError(t, err)
	assert.Contains(t, strings.Join(fake.calls[1].Args(), " "), "-a n")

	retagged := strings.Join(fake.calls[2].Args(), " ")
	assert.Contains(t, retagged, "--addtag beanstore.state=ready")
	assert.Contains(t, retagged, "--deltag beanstore.state=pushing")
	assert.Contains(t, retagged, "--deltag beanstore.transfer=tr-1")
	assert.Contains(t, retagged, "--deltag beanstore.target=10.0.0.9:50051")
}

func TestAbortPushSkipsDeactivationWhenIdle(t *testing.T) {
	fake := &fakeRunner{outputs: []string{committingIdleLV, ""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := AbortPush(t.Context(), client, testConfig(), "vol-1")

	require.NoError(t, err)
	require.Len(t, fake.calls, 2, "lookup and retag only")

	retagged := strings.Join(fake.calls[1].Args(), " ")
	assert.Contains(t, retagged, "--addtag beanstore.state=ready")
	assert.Contains(t, retagged, "--deltag beanstore.state=committing")
}

func TestAbortPushRefusesWrongState(t *testing.T) {
	fake := &fakeRunner{outputs: []string{readyLV}}
	client := lvm.New(lvm.WithRunner(fake))

	err := AbortPush(t.Context(), client, testConfig(), "vol-1")

	var wrongState *WrongStateError
	require.ErrorAs(t, err, &wrongState)
	assert.Equal(t, StateReady, wrongState.Found)
}
