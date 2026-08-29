package lvm

import (
	"context"
	"testing"

	runner "github.com/The127/go-runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRunner struct {
	calls  []*runner.Cmd
	output []byte
	err    error
}

func (r *fakeRunner) Run(_ context.Context, cmd *runner.Cmd) ([]byte, error) {
	r.calls = append(r.calls, cmd)
	return r.output, r.err
}

func TestCreatePhysicalVolumeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreatePhysicalVolume(t.Context(), "/dev/loop0", CreatePhysicalVolumeOptions{})

	require.NoError(t, err)
	require.Len(t, fake.calls, 1)
	assert.Equal(t, "lvm", fake.calls[0].Name())
	assert.Equal(t, []string{"pvcreate", "/dev/loop0"}, fake.calls[0].Args())
}

func TestCreatePhysicalVolumeForce(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreatePhysicalVolume(t.Context(), "/dev/loop0", CreatePhysicalVolumeOptions{
		Force: true,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"pvcreate", "-f", "/dev/loop0"}, fake.calls[0].Args())
}

func TestClientDevicesScopeEveryCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake), WithDevices("/dev/loop0", "/dev/loop1"))

	err := client.CreatePhysicalVolume(t.Context(), "/dev/loop0", CreatePhysicalVolumeOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"pvcreate",
		"--devices", "/dev/loop0,/dev/loop1",
		"/dev/loop0",
	}, fake.calls[0].Args())
}

func TestCallDevicesOverrideClientDevices(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake), WithDevices("/dev/loop0"))

	err := client.CreatePhysicalVolume(t.Context(), "/dev/loop7", CreatePhysicalVolumeOptions{
		CommonOptions: CommonOptions{Devices: []Device{"/dev/loop7"}},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"pvcreate",
		"--devices", "/dev/loop7",
		"/dev/loop7",
	}, fake.calls[0].Args())
}

func TestClientAutobackupAppliesToMetadataCommands(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake), WithAutobackup(false))

	err := client.ResizePhysicalVolume(t.Context(), "/dev/loop0", ResizePhysicalVolumeOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{"pvresize", "-A", "n", "/dev/loop0"}, fake.calls[0].Args())
}

func TestCallAutobackupOverridesClientAutobackup(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake), WithAutobackup(false))

	err := client.ResizePhysicalVolume(t.Context(), "/dev/loop0", ResizePhysicalVolumeOptions{
		CommonOptions: CommonOptions{Autobackup: Bool(true)},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"pvresize", "-A", "y", "/dev/loop0"}, fake.calls[0].Args())
}

func TestCallAutobackupOnNonMetadataCommandFails(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.CreatePhysicalVolume(t.Context(), "/dev/loop0", CreatePhysicalVolumeOptions{
		CommonOptions: CommonOptions{Autobackup: Bool(false)},
	})

	assert.ErrorIs(t, err, errAutobackupNotSupported)
	assert.Empty(t, fake.calls)
}

func TestClientAutobackupIsIgnoredByNonMetadataCommands(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake), WithAutobackup(false))

	err := client.CreatePhysicalVolume(t.Context(), "/dev/loop0", CreatePhysicalVolumeOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{"pvcreate", "/dev/loop0"}, fake.calls[0].Args())
}

func TestListPhysicalVolumesBuildsCommandAndParses(t *testing.T) {
	fake := &fakeRunner{output: []byte(`{
		"report": [{"pv": [
			{"pv_name": "/dev/loop0", "pv_uuid": "aaaa11-2222", "vg_name": "",
			 "pv_size": "1073741824", "pv_free": "1073741824",
			 "dev_size": "1073741824", "pv_attr": "---", "pv_tags": "",
			 "pv_allocatable": "0", "pv_exported": "0", "pv_missing": "0",
			 "pv_in_use": "0", "pv_duplicate": "0", "pv_mda_count": "1",
			 "pv_mda_used_count": "1"},
			{"pv_name": "/dev/sda2", "vg_name": "vg0", "pv_size": "512110190592",
			 "pv_free": "10737418240", "dev_size": "512110190592",
			 "pv_attr": "a--", "pv_tags": "fast,ssd", "pv_allocatable": "1",
			 "pv_exported": "0", "pv_missing": "0", "pv_in_use": "1",
			 "pv_duplicate": "0", "pv_mda_count": "1", "pv_mda_used_count": "1"}
		]}], "log": []}`)}
	client := New(WithRunner(fake))

	pvs, err := client.ListPhysicalVolumes(t.Context(), ListPhysicalVolumesOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"pvs",
		"--reportformat", "json",
		"--units", "b",
		"--nosuffix",
		"--binary",
		"-o", "pv_name,pv_uuid,vg_name,pv_size,pv_free,dev_size,pv_attr,pv_tags," +
			"pv_allocatable,pv_exported,pv_missing,pv_in_use,pv_duplicate," +
			"pv_mda_count,pv_mda_used_count",
	}, fake.calls[0].Args())

	require.Len(t, pvs, 2)
	assert.Equal(t, PhysicalVolume{
		Device:            "/dev/loop0",
		UUID:              "aaaa11-2222",
		VolumeGroup:       "",
		SizeBytes:         1073741824,
		FreeBytes:         1073741824,
		DeviceSizeBytes:   1073741824,
		Attributes:        "---",
		Tags:              nil,
		Allocatable:       false,
		MetadataAreas:     1,
		UsedMetadataAreas: 1,
	}, pvs[0])
	assert.Equal(t, []string{"fast", "ssd"}, pvs[1].Tags)
	assert.Equal(t, "vg0", pvs[1].VolumeGroup)
	assert.True(t, pvs[1].Allocatable)
	assert.True(t, pvs[1].InUse)
}

func TestListPhysicalVolumesRejectsMalformedSize(t *testing.T) {
	fake := &fakeRunner{output: []byte(`{
		"report": [{"pv": [
			{"pv_name": "/dev/loop0", "pv_size": "a lot", "pv_free": "0",
			 "dev_size": "0", "pv_mda_count": "0", "pv_mda_used_count": "0"}
		]}]}`)}
	client := New(WithRunner(fake))

	_, err := client.ListPhysicalVolumes(t.Context(), ListPhysicalVolumesOptions{})

	assert.ErrorContains(t, err, "parsing size")
}

func TestRemovePhysicalVolumeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.RemovePhysicalVolume(t.Context(), "/dev/loop0", RemovePhysicalVolumeOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{"pvremove", "/dev/loop0"}, fake.calls[0].Args())
}

func TestChangePhysicalVolumeCombinesPropertiesInOneCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ChangePhysicalVolume(t.Context(), Device("/dev/loop0"), ChangePhysicalVolumeOptions{
		AddTags:        []string{"fast", "ssd"},
		RemoveTags:     []string{"old"},
		Allocatable:    Bool(false),
		MetadataIgnore: Bool(true),
		RegenerateUUID: true,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"pvchange",
		"--addtag", "fast",
		"--addtag", "ssd",
		"--deltag", "old",
		"-x", "n",
		"--metadataignore", "y",
		"-u",
		"/dev/loop0",
	}, fake.calls[0].Args())
}

func TestChangePhysicalVolumeRequiresAProperty(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ChangePhysicalVolume(t.Context(), Device("/dev/loop0"), ChangePhysicalVolumeOptions{})

	assert.ErrorContains(t, err, "at least one property")
	assert.Empty(t, fake.calls)
}

func TestResizePhysicalVolumeToSizeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ResizePhysicalVolume(t.Context(), "/dev/loop0", ResizePhysicalVolumeOptions{
		SizeBytes: 536870912,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"pvresize",
		"--setphysicalvolumesize", "536870912b",
		"-y",
		"/dev/loop0",
	}, fake.calls[0].Args())
}

func TestRunnerErrorsPropagate(t *testing.T) {
	fake := &fakeRunner{err: assert.AnError}
	client := New(WithRunner(fake))

	err := client.CreatePhysicalVolume(t.Context(), "/dev/loop0", CreatePhysicalVolumeOptions{})

	assert.ErrorIs(t, err, assert.AnError)
}

func TestChangePhysicalVolumeBySelect(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ChangePhysicalVolume(t.Context(), Select("pv_tags = {retiring}"), ChangePhysicalVolumeOptions{
		AddTags: []string{"drained"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"pvchange",
		"--addtag", "drained",
		"-S", "pv_tags = {retiring}",
	}, fake.calls[0].Args())
}

func TestChangePhysicalVolumeAll(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ChangePhysicalVolume(t.Context(), All, ChangePhysicalVolumeOptions{
		AddTags: []string{"seen"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"pvchange", "--addtag", "seen", "-a"}, fake.calls[0].Args())
}

func TestChangePhysicalVolumeRejectsNilSelector(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.ChangePhysicalVolume(t.Context(), nil, ChangePhysicalVolumeOptions{
		AddTags: []string{"x"},
	})

	assert.ErrorContains(t, err, "nil selector")
	assert.Empty(t, fake.calls)
}

func TestListPhysicalVolumesWithSelect(t *testing.T) {
	fake := &fakeRunner{output: []byte(`{"report": [{"pv": []}]}`)}
	client := New(WithRunner(fake))

	_, err := client.ListPhysicalVolumes(t.Context(), ListPhysicalVolumesOptions{
		Select: "pv_size > 500g",
	})

	require.NoError(t, err)
	args := fake.calls[0].Args()
	assert.Equal(t, "-S", args[len(args)-2])
	assert.Equal(t, "pv_size > 500g", args[len(args)-1])
}
