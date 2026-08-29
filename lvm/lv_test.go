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

func TestCreateLogicalVolumeOptionsBuildCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreateLogicalVolume(t.Context(), "vg0", "lv0", 33554432, CreateLogicalVolumeOptions{
		Permission:           PermissionReadOnly,
		Readahead:            ReadaheadAuto,
		Contiguous:           Bool(true),
		Allocation:           AllocationCling,
		SetActivationSkip:    Bool(true),
		IgnoreActivationSkip: true,
		SetAutoactivation:    Bool(false),
		Zero:                 Bool(false),
		WipeSignatures:       Bool(false),
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"lvcreate",
		"-L", "33554432b",
		"-n", "lv0",
		"-p", "r",
		"-r", "auto",
		"-C", "y",
		"--alloc", "cling",
		"-k", "y",
		"-K",
		"--setautoactivation", "n",
		"-Z", "n",
		"-W", "n",
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

func TestCreateThinPoolOptionsBuildCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreateThinPool(t.Context(), "vg0", "pool0", 536870912, CreateThinPoolOptions{
		Activate:          Bool(true),
		Permission:        PermissionReadWrite,
		Readahead:         ReadaheadNone,
		Contiguous:        Bool(false),
		Allocation:        AllocationNormal,
		SetActivationSkip: Bool(false),
		SetAutoactivation: Bool(true),
		Zero:              Bool(false),
		Discards:          DiscardsIgnore,
		ErrorWhenFull:     Bool(true),
		PoolMetadataSpare: Bool(false),
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"lvcreate",
		"--type", "thin-pool",
		"-L", "536870912b",
		"-n", "pool0",
		"-a", "y",
		"-p", "rw",
		"-r", "none",
		"-C", "n",
		"--alloc", "normal",
		"-k", "n",
		"--setautoactivation", "y",
		"-Z", "n",
		"--discards", "ignore",
		"--errorwhenfull", "y",
		"--poolmetadataspare", "n",
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

func TestCreateThinVolumeOptionsBuildCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreateThinVolume(t.Context(), "vg0", "pool0", "vol1", 1073741824, CreateThinVolumeOptions{
		Permission:           PermissionReadOnly,
		Readahead:            ReadaheadSectors(128),
		Contiguous:           Bool(false),
		Allocation:           AllocationInherit,
		SetActivationSkip:    Bool(true),
		IgnoreActivationSkip: true,
		SetAutoactivation:    Bool(false),
		Zero:                 Bool(true),
		WipeSignatures:       Bool(true),
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"lvcreate",
		"--type", "thin",
		"--thinpool", "pool0",
		"-V", "1073741824b",
		"-n", "vol1",
		"-p", "r",
		"-r", "128",
		"-C", "n",
		"--alloc", "inherit",
		"-k", "y",
		"-K",
		"--setautoactivation", "n",
		"-Z", "y",
		"-W", "y",
		"vg0",
	}, fake.calls[0].Args())
}

func TestCreateSnapshotBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreateSnapshot(t.Context(), "vg0", "lv0", "snap0", 8388608, CreateSnapshotOptions{
		AddTags:        []string{"backup"},
		ChunkSizeBytes: 65536,
		Activate:       Bool(true),
		Permission:     PermissionReadOnly,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"lvcreate",
		"-s",
		"-L", "8388608b",
		"-n", "snap0",
		"--addtag", "backup",
		"-c", "65536b",
		"-a", "y",
		"-p", "r",
		"vg0/lv0",
	}, fake.calls[0].Args())
}

func TestCreateThinSnapshotBuildsCommands(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	require.NoError(t, client.CreateThinSnapshot(t.Context(), "vg0", "vol1", "snap1", CreateThinSnapshotOptions{
		IgnoreActivationSkip: true,
	}))
	require.NoError(t, client.CreateThinSnapshot(t.Context(), "vg0", "ext0", "snap2", CreateThinSnapshotOptions{
		Pool:              "pool0",
		SetActivationSkip: Bool(false),
	}))

	assert.Equal(t, []string{
		"lvcreate",
		"-s",
		"-n", "snap1",
		"-K",
		"vg0/vol1",
	}, fake.calls[0].Args())
	assert.Equal(t, []string{
		"lvcreate",
		"-s",
		"-n", "snap2",
		"--thinpool", "pool0",
		"-k", "n",
		"vg0/ext0",
	}, fake.calls[1].Args())
}

func TestListLogicalVolumesBuildsCommandAndParses(t *testing.T) {
	fake := &fakeRunner{output: []byte(`{
		"report": [{"lv": [
			{"lv_name": "pool0", "lv_uuid": "uuid-p", "vg_name": "vg0",
			 "lv_size": "536870912", "lv_metadata_size": "4194304",
			 "lv_attr": "twi-aotz--", "lv_tags": "",
			 "pool_lv": "", "origin": "", "lv_path": "", "lv_dm_path": "/dev/mapper/vg0-pool0",
			 "data_percent": "7.42", "metadata_percent": "10.94",
			 "lv_active": "active", "lv_layout": "pool,thin"},
			{"lv_name": "vol1", "lv_uuid": "uuid-v", "vg_name": "vg0",
			 "lv_size": "1073741824", "lv_attr": "Vwi---tz--",
			 "lv_tags": "state.creating,beanstore", "pool_lv": "pool0",
			 "origin": "", "lv_path": "/dev/vg0/vol1", "lv_dm_path": "/dev/mapper/vg0-vol1",
			 "data_percent": "", "metadata_percent": "",
			 "lv_active": "", "lv_layout": "thin,sparse"}
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
		"-o", "lv_name,lv_uuid,vg_name,lv_size,lv_metadata_size,chunk_size,lv_attr,lv_tags,pool_lv,origin," +
			"lv_path,lv_dm_path,data_percent,metadata_percent,lv_active,lv_layout",
		"-S", "lv_tags = {beanstore}",
		"vg0",
	}, fake.calls[0].Args())

	require.Len(t, lvs, 2)
	assert.Equal(t, LogicalVolume{
		Name:              "pool0",
		UUID:              "uuid-p",
		VolumeGroup:       "vg0",
		SizeBytes:         536870912,
		MetadataSizeBytes: 4194304,
		Attributes:        "twi-aotz--",
		DevicePath:        "/dev/mapper/vg0-pool0",
		DataPercent:       7.42,
		MetadataPercent:   10.94,
		Active:            true,
		Layout:            []string{"pool", "thin"},
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
	require.NoError(t, client.RemoveLogicalVolume(t.Context(), Name("vg0/vol1"), RemoveLogicalVolumeOptions{NoHistory: true}))

	assert.Equal(t, []string{"lvremove", "vg0/vol1"}, fake.calls[0].Args())
	assert.Equal(t, []string{"lvremove", "-f", "-S", "lv_tags = {gone}"}, fake.calls[1].Args())
	assert.Equal(t, []string{"lvremove", "--nohistory", "vg0/vol1"}, fake.calls[2].Args())
}

func TestListLogicalVolumesAllAndHistory(t *testing.T) {
	fake := &fakeRunner{output: []byte(`{
		"report": [{"lv": [
			{"lv_name": "[pool0_tdata]", "lv_uuid": "uuid-d", "vg_name": "vg0",
			 "lv_size": "536870912", "lv_attr": "Twi-ao----", "lv_tags": "",
			 "pool_lv": "", "origin": "", "lv_path": "", "lv_dm_path": "",
			 "data_percent": "", "metadata_percent": "",
			 "lv_active": "", "lv_layout": "linear"},
			{"lv_name": "-vol9", "lv_uuid": "", "vg_name": "vg0",
			 "lv_size": "", "lv_attr": "-hi-------", "lv_tags": "",
			 "pool_lv": "pool0", "origin": "", "lv_path": "", "lv_dm_path": "",
			 "data_percent": "", "metadata_percent": "",
			 "lv_active": "", "lv_layout": ""}
		]}], "log": []}`)}
	client := New(WithRunner(fake))

	lvs, err := client.ListLogicalVolumes(t.Context(), ListLogicalVolumesOptions{
		All:     true,
		History: true,
	})

	require.NoError(t, err)
	assert.Contains(t, fake.calls[0].Args(), "-a")
	assert.Contains(t, fake.calls[0].Args(), "-H")
	require.Len(t, lvs, 2)
	assert.Equal(t, "[pool0_tdata]", lvs[0].Name)
	assert.Equal(t, "-vol9", lvs[1].Name)
	assert.Zero(t, lvs[1].SizeBytes)
}

func TestChangeLogicalVolumeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ChangeLogicalVolume(t.Context(), Name("vg0/vol1"), ChangeLogicalVolumeOptions{
		AddTags:    []string{"state.ready"},
		RemoveTags: []string{"state.creating"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"lvchange",
		"--addtag", "state.ready",
		"--deltag", "state.creating",
		"vg0/vol1",
	}, fake.calls[0].Args())
}

func TestChangeLogicalVolumePropertiesBuildCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ChangeLogicalVolume(t.Context(), Name("vg0/vol1"), ChangeLogicalVolumeOptions{
		Permission:        PermissionReadOnly,
		Contiguous:        Bool(true),
		Zero:              Bool(false),
		Discards:          DiscardsNoPassdown,
		ErrorWhenFull:     Bool(true),
		SetActivationSkip: Bool(true),
		Readahead:         ReadaheadSectors(256),
		Allocation:        AllocationAnywhere,
		SetAutoactivation: Bool(false),
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"lvchange",
		"-p", "r",
		"-C", "y",
		"-Z", "n",
		"--discards", "nopassdown",
		"--errorwhenfull", "y",
		"-k", "y",
		"-r", "256",
		"--alloc", "anywhere",
		"--setautoactivation", "n",
		"vg0/vol1",
	}, fake.calls[0].Args())
}

func TestChangeLogicalVolumeRequiresAProperty(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ChangeLogicalVolume(t.Context(), Name("vg0/vol1"), ChangeLogicalVolumeOptions{})

	assert.ErrorContains(t, err, "at least one property")
	assert.Empty(t, fake.calls)
}

func TestActivateAndDeactivateLogicalVolumeBuildCommands(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	require.NoError(t, client.ActivateLogicalVolume(t.Context(), Name("vg0/vol1"), ActivateLogicalVolumeOptions{
		IgnoreActivationSkip: true,
	}))
	require.NoError(t, client.DeactivateLogicalVolume(t.Context(), Select("lv_tags = {beanstore}"), DeactivateLogicalVolumeOptions{}))

	assert.Equal(t, []string{"lvchange", "-a", "y", "-K", "vg0/vol1"}, fake.calls[0].Args())
	assert.Equal(t, []string{"lvchange", "-a", "n", "-S", "lv_tags = {beanstore}"}, fake.calls[1].Args())
}

func TestExtendLogicalVolumeBuildsCommands(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	require.NoError(t, client.ExtendLogicalVolume(t.Context(), "vg0/vol1", Bytes(128<<20), ExtendLogicalVolumeOptions{}))
	require.NoError(t, client.ExtendLogicalVolume(t.Context(), "vg0/vol1", GrowBy(Bytes(32<<20)), ExtendLogicalVolumeOptions{
		ResizeFilesystem:      true,
		PoolMetadataSizeBytes: 8 << 20,
		Stripes:               2,
		StripeSizeBytes:       64 << 10,
		Allocation:            AllocationAnywhere,
	}))
	require.NoError(t, client.ExtendLogicalVolume(t.Context(), "vg0/vol1", GrowBy(Percent(100, PercentFree)), ExtendLogicalVolumeOptions{}))
	require.NoError(t, client.ExtendLogicalVolume(t.Context(), "vg0/vol1", Extents(50), ExtendLogicalVolumeOptions{}))

	assert.Equal(t, []string{"lvextend", "-L", "134217728b", "vg0/vol1"}, fake.calls[0].Args())
	assert.Equal(t, []string{
		"lvextend",
		"-L", "+33554432b",
		"-r",
		"--poolmetadatasize", "8388608b",
		"-i", "2",
		"-I", "65536b",
		"--alloc", "anywhere",
		"vg0/vol1",
	}, fake.calls[1].Args())
	assert.Equal(t, []string{"lvextend", "-l", "+100%FREE", "vg0/vol1"}, fake.calls[2].Args())
	assert.Equal(t, []string{"lvextend", "-l", "50", "vg0/vol1"}, fake.calls[3].Args())

	err := client.ExtendLogicalVolume(t.Context(), "vg0/vol1", ShrinkBy(Bytes(1)), ExtendLogicalVolumeOptions{})
	assert.ErrorContains(t, err, "cannot use ShrinkBy")
	assert.Len(t, fake.calls, 4)
}

func TestExtendLogicalVolumeByPolicyBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	require.NoError(t, client.ExtendLogicalVolumeByPolicy(t.Context(), "vg0/pool0", ExtendLogicalVolumeByPolicyOptions{}))

	assert.Equal(t, []string{"lvextend", "--usepolicies", "vg0/pool0"}, fake.calls[0].Args())
}

func TestReduceLogicalVolumeBuildsCommands(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	require.NoError(t, client.ReduceLogicalVolume(t.Context(), "vg0/vol1", Bytes(64<<20), ReduceLogicalVolumeOptions{}))
	require.NoError(t, client.ReduceLogicalVolume(t.Context(), "vg0/vol1", ShrinkBy(Bytes(16<<20)), ReduceLogicalVolumeOptions{}))

	assert.Equal(t, []string{"lvreduce", "-L", "67108864b", "-f", "vg0/vol1"}, fake.calls[0].Args())
	assert.Equal(t, []string{"lvreduce", "-L", "-16777216b", "-f", "vg0/vol1"}, fake.calls[1].Args())

	err := client.ReduceLogicalVolume(t.Context(), "vg0/vol1", GrowBy(Bytes(1)), ReduceLogicalVolumeOptions{})
	assert.ErrorContains(t, err, "cannot use GrowBy")
	assert.Len(t, fake.calls, 2)
}

func TestResizeLogicalVolumeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ResizeLogicalVolume(t.Context(), "vg0/vol1", Bytes(256<<20), ResizeLogicalVolumeOptions{
		ResizeFilesystem: true,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"lvresize", "-L", "268435456b", "-r", "vg0/vol1"}, fake.calls[0].Args())

	err = client.ResizeLogicalVolume(t.Context(), "vg0/vol1", GrowBy(Bytes(1)), ResizeLogicalVolumeOptions{})
	assert.ErrorContains(t, err, "absolute size")
	assert.Len(t, fake.calls, 1)
}

func TestRenameLogicalVolumeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.RenameLogicalVolume(t.Context(), "vg0", "vol1", "vol2", RenameLogicalVolumeOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{"lvrename", "vg0", "vol1", "vol2"}, fake.calls[0].Args())
}

func TestDisplayLogicalVolumeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{output: []byte("  --- Logical volume ---\n")}
	client := New(WithRunner(fake))

	output, err := client.DisplayLogicalVolume(t.Context(), "vg0/vol1", DisplayLogicalVolumeOptions{Maps: true})

	require.NoError(t, err)
	assert.Equal(t, "  --- Logical volume ---\n", output)
	assert.Equal(t, []string{"lvdisplay", "-m", "vg0/vol1"}, fake.calls[0].Args())
}

func TestScanLogicalVolumesBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	require.NoError(t, client.ScanLogicalVolumes(t.Context(), ScanLogicalVolumesOptions{}))
	require.NoError(t, client.ScanLogicalVolumes(t.Context(), ScanLogicalVolumesOptions{All: true}))

	assert.Equal(t, []string{"lvscan"}, fake.calls[0].Args())
	assert.Equal(t, []string{"lvscan", "-a"}, fake.calls[1].Args())
}
