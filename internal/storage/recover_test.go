package storage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/The127/beanstore/lvm"
)

const mixedStates = `{"report": [{"lv": [
	{"lv_name": "vol-creating", "lv_uuid": "u1", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=creating", "pool_lv": "pool0", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "",
	 "metadata_percent": "", "lv_active": "", "lv_layout": "thin,sparse"},
	{"lv_name": "vol-ready", "lv_uuid": "u2", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=ready", "pool_lv": "pool0", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "",
	 "metadata_percent": "", "lv_active": "", "lv_layout": "thin,sparse"},
	{"lv_name": "vol-attached", "lv_uuid": "u3", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=attached", "pool_lv": "pool0", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "",
	 "metadata_percent": "", "lv_active": "", "lv_layout": "thin,sparse"},
	{"lv_name": "vol-deleting", "lv_uuid": "u4", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=deleting", "pool_lv": "pool0", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "",
	 "metadata_percent": "", "lv_active": "", "lv_layout": "thin,sparse"}
]}], "log": []}`

func TestRecoverAppliesCrashRules(t *testing.T) {
	fake := &fakeRunner{outputs: []string{mixedStates, "", "", ""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := Recover(t.Context(), client, testConfig())

	require.NoError(t, err)
	require.Len(t, fake.calls, 4, "scan plus one command per non-ready volume")
	assert.Equal(t, []string{"lvremove", "-f", "vg0/vol-creating"}, fake.calls[1].Args())
	assert.Contains(t, strings.Join(fake.calls[2].Args(), " "), "-a y")
	assert.Contains(t, strings.Join(fake.calls[2].Args(), " "), "vg0/vol-attached")
	assert.Equal(t, []string{"lvremove", "-f", "vg0/vol-deleting"}, fake.calls[3].Args())
}

const pushStates = `{"report": [{"lv": [
	{"lv_name": "vol-pushing", "lv_uuid": "u1", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vwi-a-tz--",
	 "lv_tags": "beanstore.state=pushing,beanstore.transfer=tr-1,beanstore.target=10.0.0.9:50051",
	 "pool_lv": "pool0", "origin": "", "lv_path": "/dev/vg0/vol-pushing",
	 "lv_dm_path": "", "data_percent": "1.00", "metadata_percent": "",
	 "lv_active": "active", "lv_layout": "thin,sparse"},
	{"lv_name": "vol-committing", "lv_uuid": "u2", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=committing,beanstore.transfer=tr-2,beanstore.target=10.0.0.9:50051",
	 "pool_lv": "pool0", "origin": "", "lv_path": "", "lv_dm_path": "",
	 "data_percent": "", "metadata_percent": "", "lv_active": "",
	 "lv_layout": "thin,sparse"}
]}], "log": []}`

const pushingSingle = `{"report": [{"lv": [
	{"lv_name": "vol-pushing", "lv_uuid": "u1", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vwi-a-tz--",
	 "lv_tags": "beanstore.state=pushing,beanstore.transfer=tr-1,beanstore.target=10.0.0.9:50051",
	 "pool_lv": "pool0", "origin": "", "lv_path": "/dev/vg0/vol-pushing",
	 "lv_dm_path": "", "data_percent": "1.00", "metadata_percent": "",
	 "lv_active": "active", "lv_layout": "thin,sparse"}
]}], "log": []}`

// rollbackScan renders one rollback copy named name targeting vol-1,
// optionally next to a surviving vol-1.
func rollbackScan(name string, withTarget bool) string {
	rows := `{"lv_name": "` + name + `", "lv_uuid": "u1", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=rollback,beanstore.rollback_target=vol-1",
	 "pool_lv": "pool0", "origin": "snap-1", "lv_path": "", "lv_dm_path": "",
	 "data_percent": "", "metadata_percent": "", "lv_active": "",
	 "lv_layout": "thin,sparse"}`
	if withTarget {
		rows += `,
	{"lv_name": "vol-1", "lv_uuid": "u2", "vg_name": "vg0",
	 "lv_size": "1048576", "lv_attr": "Vwi---tz--",
	 "lv_tags": "beanstore.state=ready", "pool_lv": "pool0", "origin": "",
	 "lv_path": "", "lv_dm_path": "", "data_percent": "",
	 "metadata_percent": "", "lv_active": "", "lv_layout": "thin,sparse"}`
	}

	return `{"report": [{"lv": [` + rows + `]}], "log": []}`
}

func TestRecoverSettlesRollbackCopies(t *testing.T) {
	// rule 1: already renamed, retag only
	fake := &fakeRunner{outputs: []string{rollbackScan("vol-1", false), ""}}
	require.NoError(t, Recover(t.Context(), lvm.New(lvm.WithRunner(fake)), testConfig()))
	require.Len(t, fake.calls, 2)
	retagged := strings.Join(fake.calls[1].Args(), " ")
	assert.Contains(t, retagged, "--addtag beanstore.state=ready")
	assert.NotContains(t, strings.Join(fake.commands(), " "), "lvrename")

	// rule 2: the target survived, the rollback aborts
	fake = &fakeRunner{outputs: []string{rollbackScan("vol-1+rb", true), ""}}
	require.NoError(t, Recover(t.Context(), lvm.New(lvm.WithRunner(fake)), testConfig()))
	assert.Contains(t, fake.commands(), "lvremove -f vg0/vol-1+rb")

	// rule 3: the target is gone, the rollback finishes
	fake = &fakeRunner{outputs: []string{rollbackScan("vol-1+rb", false), "", ""}}
	require.NoError(t, Recover(t.Context(), lvm.New(lvm.WithRunner(fake)), testConfig()))
	commands := fake.commands()
	require.Len(t, commands, 3)
	assert.Equal(t, "lvrename vg0 vol-1+rb vol-1", commands[1])
	assert.Contains(t, commands[2], "--addtag beanstore.state=ready")
}

func TestRecoverBackfillsSnapshotLineage(t *testing.T) {
	fake := &fakeRunner{outputs: []string{lineageLVs, ""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := Recover(t.Context(), client, testConfig())

	require.NoError(t, err)
	require.Len(t, fake.calls, 2, "one backfill for the legacy snapshot only")
	backfilled := strings.Join(fake.calls[1].Args(), " ")
	assert.Contains(t, backfilled, "--addtag beanstore.origin=vol-1")
	assert.Contains(t, backfilled, "vg0/snap-legacy")
}

func TestRecoverRevertsPushingKeepsCommitting(t *testing.T) {
	fake := &fakeRunner{outputs: []string{pushStates, pushingSingle, "", ""}}
	client := lvm.New(lvm.WithRunner(fake))

	err := Recover(t.Context(), client, testConfig())

	require.NoError(t, err)
	require.Len(t, fake.calls, 4, "scan, lookup, deactivate, retag, nothing for committing")
	deactivated := strings.Join(fake.calls[2].Args(), " ")
	assert.Contains(t, deactivated, "-a n")
	assert.Contains(t, deactivated, "vg0/vol-pushing")

	retagged := strings.Join(fake.calls[3].Args(), " ")
	assert.Contains(t, retagged, "--addtag beanstore.state=ready")
	assert.Contains(t, retagged, "--deltag beanstore.state=pushing")
	assert.Contains(t, retagged, "--deltag beanstore.transfer=tr-1")
	assert.Contains(t, retagged, "--deltag beanstore.target=10.0.0.9:50051")
}
