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
