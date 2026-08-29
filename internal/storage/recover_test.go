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
