package lvm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateLogicalVolumeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreateLogicalVolume(t.Context(), "vg0", "lv0", 33554432, CreateLogicalVolumeOptions{
		AddTags:  []string{"beanstore"},
		Activate: Bool(false),
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"lvcreate",
		"-L", "33554432b",
		"-n", "lv0",
		"--addtag", "beanstore",
		"-a", "n",
		"vg0",
	}, fake.calls[0].Args())
}

func TestCreateThinPoolBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreateThinPool(t.Context(), "vg0", "pool0", 536870912, CreateThinPoolOptions{
		ChunkSizeBytes:    65536,
		MetadataSizeBytes: 4194304,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"lvcreate",
		"--type", "thin-pool",
		"-L", "536870912b",
		"-n", "pool0",
		"-c", "65536b",
		"--poolmetadatasize", "4194304b",
		"vg0",
	}, fake.calls[0].Args())
}

func TestCreateThinVolumeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreateThinVolume(t.Context(), "vg0", "pool0", "vol1", 1073741824, CreateThinVolumeOptions{
		AddTags:  []string{"state.creating"},
		Activate: Bool(false),
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"lvcreate",
		"--type", "thin",
		"--thinpool", "pool0",
		"-V", "1073741824b",
		"-n", "vol1",
		"--addtag", "state.creating",
		"-a", "n",
		"vg0",
	}, fake.calls[0].Args())
}

func TestListLogicalVolumesBuildsCommandAndParses(t *testing.T) {
	fake := &fakeRunner{output: []byte(`{
		"report": [{"lv": [
			{"lv_name": "pool0", "lv_uuid": "uuid-p", "vg_name": "vg0",
			 "lv_size": "536870912", "lv_attr": "twi-aotz--", "lv_tags": "",
			 "pool_lv": "", "origin": "", "lv_path": "", "lv_dm_path": "/dev/mapper/vg0-pool0",
			 "data_percent": "7.42", "metadata_percent": "10.94",
			 "lv_active": "1", "lv_layout": "pool,thin"},
			{"lv_name": "vol1", "lv_uuid": "uuid-v", "vg_name": "vg0",
			 "lv_size": "1073741824", "lv_attr": "Vwi---tz--",
			 "lv_tags": "state.creating,beanstore", "pool_lv": "pool0",
			 "origin": "", "lv_path": "/dev/vg0/vol1", "lv_dm_path": "/dev/mapper/vg0-vol1",
			 "data_percent": "", "metadata_percent": "",
			 "lv_active": "0", "lv_layout": "thin,sparse"}
		]}], "log": []}`)}
	client := New(WithRunner(fake))

	lvs, err := client.ListLogicalVolumes(t.Context(), ListLogicalVolumesOptions{
		VG:     "vg0",
		Select: "lv_tags = {beanstore}",
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"lvs",
		"--reportformat", "json",
		"--units", "b",
		"--nosuffix",
		"--binary",
		"-o", "lv_name,lv_uuid,vg_name,lv_size,lv_attr,lv_tags,pool_lv,origin," +
			"lv_path,lv_dm_path,data_percent,metadata_percent,lv_active,lv_layout",
		"-S", "lv_tags = {beanstore}",
		"vg0",
	}, fake.calls[0].Args())

	require.Len(t, lvs, 2)
	assert.Equal(t, LogicalVolume{
		Name:            "pool0",
		UUID:            "uuid-p",
		VolumeGroup:     "vg0",
		SizeBytes:       536870912,
		Attributes:      "twi-aotz--",
		DevicePath:      "/dev/mapper/vg0-pool0",
		DataPercent:     7.42,
		MetadataPercent: 10.94,
		Active:          true,
		Layout:          []string{"pool", "thin"},
	}, lvs[0])
	assert.Equal(t, LogicalVolume{
		Name:        "vol1",
		UUID:        "uuid-v",
		VolumeGroup: "vg0",
		SizeBytes:   1073741824,
		Attributes:  "Vwi---tz--",
		Tags:        []string{"state.creating", "beanstore"},
		Pool:        "pool0",
		Path:        "/dev/vg0/vol1",
		DevicePath:  "/dev/mapper/vg0-vol1",
		Layout:      []string{"thin", "sparse"},
	}, lvs[1])
}

func TestRemoveLogicalVolumeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	require.NoError(t, client.RemoveLogicalVolume(t.Context(), Name("vg0/vol1"), RemoveLogicalVolumeOptions{}))
	require.NoError(t, client.RemoveLogicalVolume(t.Context(), Select("lv_tags = {gone}"), RemoveLogicalVolumeOptions{Force: true}))

	assert.Equal(t, []string{"lvremove", "vg0/vol1"}, fake.calls[0].Args())
	assert.Equal(t, []string{"lvremove", "-f", "-S", "lv_tags = {gone}"}, fake.calls[1].Args())
}
