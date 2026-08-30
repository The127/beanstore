package storage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/The127/beanstore/lvm"
)

const wrongLineageSnapLV = `{"report": [{"lv": [
	{"lv_name": "snap-1", "lv_uuid": "uuid-s", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vri---tz--",
	 "lv_tags": "beanstore.state=snapshot,beanstore.origin=vol-other",
	 "pool_lv": "pool0", "origin": "", "lv_path": "", "lv_dm_path": "",
	 "data_percent": "", "metadata_percent": "", "lv_active": "",
	 "lv_layout": "thin,sparse"}
]}], "log": []}`

func TestBeginRollbackCreatesTaggedCopy(t *testing.T) {
	fake := &fakeRunner{outputs: []string{readyLV, snapshotLV, ""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := BeginRollback(t.Context(), client, testConfig(), "vol-1", "snap-1")

	require.NoError(t, err)
	created := strings.Join(fake.calls[2].Args(), " ")
	assert.Contains(t, created, "lvcreate -s -n vol-1+rb")
	assert.Contains(t, created, "--addtag beanstore.state=rollback")
	assert.Contains(t, created, "--addtag beanstore.rollback_target=vol-1")
	assert.Contains(t, created, "-p rw")
	assert.Contains(t, created, "vg0/snap-1")
}

func TestBeginRollbackRefusesWrongLineage(t *testing.T) {
	fake := &fakeRunner{outputs: []string{readyLV, wrongLineageSnapLV}}
	client := lvm.New(lvm.WithRunner(fake))

	err := BeginRollback(t.Context(), client, testConfig(), "vol-1", "snap-1")

	assert.ErrorIs(t, err, ErrWrongLineage)
	assert.Len(t, fake.calls, 2, "no copy was created")
}

func TestBeginRollbackRefusesWrongStates(t *testing.T) {
	fake := &fakeRunner{outputs: []string{attachedLV}}
	client := lvm.New(lvm.WithRunner(fake))

	err := BeginRollback(t.Context(), client, testConfig(), "vol-1", "snap-1")

	var wrongState *WrongStateError
	require.ErrorAs(t, err, &wrongState)
	assert.Equal(t, StateAttached, wrongState.Found)

	fake = &fakeRunner{outputs: []string{readyLV, readyLV}}
	client = lvm.New(lvm.WithRunner(fake))

	err = BeginRollback(t.Context(), client, testConfig(), "vol-1", "vol-2")

	require.ErrorAs(t, err, &wrongState)
	assert.Equal(t, StateReady, wrongState.Found)
}

func TestFinishRollbackRenamesAndRetags(t *testing.T) {
	fake := &fakeRunner{}
	client := lvm.New(lvm.WithRunner(fake))

	err := FinishRollback(t.Context(), client, testConfig(), "vol-1+rb", "vol-1")

	require.NoError(t, err)
	commands := fake.commands()
	require.Len(t, commands, 2)
	assert.Equal(t, "lvrename vg0 vol-1+rb vol-1", commands[0])
	assert.Contains(t, commands[1], "--addtag beanstore.state=ready")
	assert.Contains(t, commands[1], "--deltag beanstore.state=rollback")
	assert.Contains(t, commands[1], "--deltag beanstore.rollback_target=vol-1")
	assert.Contains(t, commands[1], "-k n")
}

func TestFinishRollbackRetagOnly(t *testing.T) {
	fake := &fakeRunner{}
	client := lvm.New(lvm.WithRunner(fake))

	err := FinishRollback(t.Context(), client, testConfig(), "vol-1", "vol-1")

	require.NoError(t, err)
	commands := fake.commands()
	require.Len(t, commands, 1, "an already renamed copy only retags")
	assert.Contains(t, commands[0], "--addtag beanstore.state=ready")
}
