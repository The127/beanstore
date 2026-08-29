package lvm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateVolumeGroupBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreateVolumeGroup(t.Context(), "vg0", []Device{"/dev/loop0", "/dev/loop1"}, CreateVolumeGroupOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{"vgcreate", "vg0", "/dev/loop0", "/dev/loop1"}, fake.calls[0].Args())
}

func TestCreateVolumeGroupAllFlags(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreateVolumeGroup(t.Context(), "vg0", []Device{"/dev/loop0"}, CreateVolumeGroupOptions{
		AddTags:           []string{"fast", "beanstore"},
		ExtentSizeBytes:   4194304,
		SetAutoactivation: Bool(false),
		Force:             true,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"vgcreate",
		"--addtag", "fast",
		"--addtag", "beanstore",
		"-s", "4194304b",
		"--setautoactivation", "n",
		"-f",
		"vg0",
		"/dev/loop0",
	}, fake.calls[0].Args())
}

func TestListVolumeGroupsBuildsCommandAndParses(t *testing.T) {
	fake := &fakeRunner{output: []byte(`{
		"report": [{"vg": [
			{"vg_name": "vg0", "vg_uuid": "uuid-1", "vg_size": "1069547520",
			 "vg_free": "1069547520", "vg_extent_size": "4194304",
			 "vg_extent_count": "255", "vg_free_count": "255", "pv_count": "1",
			 "lv_count": "0", "snap_count": "0", "vg_missing_pv_count": "0",
			 "vg_tags": "fast,beanstore", "vg_attr": "wz--n-", "vg_exported": "0",
			 "vg_partial": "0", "vg_shared": "0", "vg_autoactivation": "1"}
		]}], "log": []}`)}
	client := New(WithRunner(fake))

	vgs, err := client.ListVolumeGroups(t.Context(), ListVolumeGroupsOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"vgs",
		"--reportformat", "json",
		"--units", "b",
		"--nosuffix",
		"--binary",
		"-o", "vg_name,vg_uuid,vg_size,vg_free,vg_extent_size,vg_extent_count," +
			"vg_free_count,pv_count,lv_count,snap_count,vg_missing_pv_count," +
			"vg_tags,vg_attr,vg_exported,vg_partial,vg_shared,vg_autoactivation",
	}, fake.calls[0].Args())
	require.Len(t, vgs, 1)
	assert.Equal(t, VolumeGroup{
		Name:            "vg0",
		UUID:            "uuid-1",
		SizeBytes:       1069547520,
		FreeBytes:       1069547520,
		ExtentSizeBytes: 4194304,
		Extents:         255,
		FreeExtents:     255,
		PVCount:         1,
		Tags:            []string{"fast", "beanstore"},
		Attributes:      "wz--n-",
		Autoactivation:  true,
	}, vgs[0])
}

func TestListVolumeGroupsWithSelect(t *testing.T) {
	fake := &fakeRunner{output: []byte(`{"report": [{"vg": []}]}`)}
	client := New(WithRunner(fake))

	_, err := client.ListVolumeGroups(t.Context(), ListVolumeGroupsOptions{
		Select: "vg_tags = {beanstore}",
	})

	require.NoError(t, err)
	args := fake.calls[0].Args()
	assert.Equal(t, "-S", args[len(args)-2])
	assert.Equal(t, "vg_tags = {beanstore}", args[len(args)-1])
}

func TestRemoveVolumeGroupBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	require.NoError(t, client.RemoveVolumeGroup(t.Context(), "vg0", RemoveVolumeGroupOptions{}))
	require.NoError(t, client.RemoveVolumeGroup(t.Context(), "vg0", RemoveVolumeGroupOptions{Force: true}))

	assert.Equal(t, []string{"vgremove", "vg0"}, fake.calls[0].Args())
	assert.Equal(t, []string{"vgremove", "-f", "vg0"}, fake.calls[1].Args())
}

func TestVolumeGroupAutobackupOnPlainCommandsFails(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	_, err := client.ListVolumeGroups(t.Context(), ListVolumeGroupsOptions{
		CommonOptions: CommonOptions{Autobackup: Bool(false)},
	})
	assert.ErrorIs(t, err, errAutobackupNotSupported)

	err = client.RemoveVolumeGroup(t.Context(), "vg0", RemoveVolumeGroupOptions{
		CommonOptions: CommonOptions{Autobackup: Bool(false)},
	})
	assert.ErrorIs(t, err, errAutobackupNotSupported)
	assert.Empty(t, fake.calls)
}

func TestExtendVolumeGroupBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ExtendVolumeGroup(t.Context(), "vg0", []Device{"/dev/loop1"}, ExtendVolumeGroupOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{"vgextend", "vg0", "/dev/loop1"}, fake.calls[0].Args())
}

func TestExtendVolumeGroupFlags(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ExtendVolumeGroup(t.Context(), "vg0", []Device{"/dev/loop1"}, ExtendVolumeGroupOptions{
		Force:          true,
		RestoreMissing: true,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"vgextend", "-f", "--restoremissing", "vg0", "/dev/loop1"}, fake.calls[0].Args())
}

func TestReduceVolumeGroupByDevices(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ReduceVolumeGroup(t.Context(), "vg0", []Device{"/dev/loop1"}, ReduceVolumeGroupOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{"vgreduce", "vg0", "/dev/loop1"}, fake.calls[0].Args())
}

func TestReduceVolumeGroupForms(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	require.NoError(t, client.ReduceVolumeGroup(t.Context(), "vg0", nil, ReduceVolumeGroupOptions{RemoveUnused: true}))
	require.NoError(t, client.ReduceVolumeGroup(t.Context(), "vg0", nil, ReduceVolumeGroupOptions{RemoveMissing: true, Force: true}))

	assert.Equal(t, []string{"vgreduce", "-a", "vg0"}, fake.calls[0].Args())
	assert.Equal(t, []string{"vgreduce", "--removemissing", "-f", "vg0"}, fake.calls[1].Args())
}

func TestReduceVolumeGroupRequiresExactlyOneForm(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ReduceVolumeGroup(t.Context(), "vg0", nil, ReduceVolumeGroupOptions{})
	assert.ErrorContains(t, err, "exactly one")

	err = client.ReduceVolumeGroup(t.Context(), "vg0", []Device{"/dev/loop1"}, ReduceVolumeGroupOptions{RemoveUnused: true})
	assert.ErrorContains(t, err, "exactly one")
	assert.Empty(t, fake.calls)
}

func TestRenameVolumeGroupBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.RenameVolumeGroup(t.Context(), "vg0", "vg1", RenameVolumeGroupOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{"vgrename", "vg0", "vg1"}, fake.calls[0].Args())
}

func TestChangeVolumeGroupCombinesProperties(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ChangeVolumeGroup(t.Context(), Name("vg0"), ChangeVolumeGroupOptions{
		AddTags:            []string{"fast"},
		RemoveTags:         []string{"old"},
		Resizeable:         Bool(false),
		MaxLogicalVolumes:  ptrUint64(10),
		MaxPhysicalVolumes: ptrUint64(0),
		ExtentSizeBytes:    8388608,
		RegenerateUUID:     true,
		SetAutoactivation:  Bool(false),
		Allocation:         AllocationCling,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"vgchange",
		"--addtag", "fast",
		"--deltag", "old",
		"-x", "n",
		"-l", "10",
		"-p", "0",
		"-s", "8388608b",
		"-u",
		"--setautoactivation", "n",
		"--alloc", "cling",
		"vg0",
	}, fake.calls[0].Args())
}

func ptrUint64(v uint64) *uint64 {
	return &v
}

func TestChangeVolumeGroupBySelect(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ChangeVolumeGroup(t.Context(), Select("vg_tags = {beanstore}"), ChangeVolumeGroupOptions{
		AddTags: []string{"seen"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"vgchange", "--addtag", "seen", "-S", "vg_tags = {beanstore}",
	}, fake.calls[0].Args())
}

func TestChangeVolumeGroupRequiresAProperty(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ChangeVolumeGroup(t.Context(), Name("vg0"), ChangeVolumeGroupOptions{})

	assert.ErrorContains(t, err, "at least one property")
	assert.Empty(t, fake.calls)
}

func TestActivateVolumeGroupBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ActivateVolumeGroup(t.Context(), Name("vg0"), ActivateVolumeGroupOptions{
		IgnoreActivationSkip: true,
		Mode:                 ActivationDegraded,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"vgchange", "-a", "y", "-K", "--activationmode", "degraded", "vg0",
	}, fake.calls[0].Args())
}

func TestDeactivateVolumeGroupBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.DeactivateVolumeGroup(t.Context(), All, DeactivateVolumeGroupOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{"vgchange", "-a", "n"}, fake.calls[0].Args())
}

func TestCheckVolumeGroupBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	require.NoError(t, client.CheckVolumeGroup(t.Context(), "vg0", CheckVolumeGroupOptions{}))
	require.NoError(t, client.CheckVolumeGroup(t.Context(), "", CheckVolumeGroupOptions{}))

	assert.Equal(t, []string{"vgck", "vg0"}, fake.calls[0].Args())
	assert.Equal(t, []string{"vgck"}, fake.calls[1].Args())
}

func TestDisplayVolumeGroupBuildsCommand(t *testing.T) {
	fake := &fakeRunner{output: []byte("  --- Volume group ---\n")}
	client := New(WithRunner(fake))

	output, err := client.DisplayVolumeGroup(t.Context(), "vg0", DisplayVolumeGroupOptions{Short: true})

	require.NoError(t, err)
	assert.Equal(t, "  --- Volume group ---\n", output)
	assert.Equal(t, []string{"vgdisplay", "-s", "vg0"}, fake.calls[0].Args())
}

func TestScanVolumeGroupsBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	require.NoError(t, client.ScanVolumeGroups(t.Context(), ScanVolumeGroupsOptions{}))
	require.NoError(t, client.ScanVolumeGroups(t.Context(), ScanVolumeGroupsOptions{MkNodes: true}))

	assert.Equal(t, []string{"vgscan"}, fake.calls[0].Args())
	assert.Equal(t, []string{"vgscan", "--mknodes"}, fake.calls[1].Args())
}
