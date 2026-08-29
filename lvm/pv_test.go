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

	err := client.CreatePhysicalVolume(t.Context(), "/dev/loop0")

	require.NoError(t, err)
	require.Len(t, fake.calls, 1)
	assert.Equal(t, "lvm", fake.calls[0].Name())
	assert.Equal(t, []string{"pvcreate", "/dev/loop0"}, fake.calls[0].Args())
}

func TestWithDevicesScopesEveryCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake), WithDevices("/dev/loop0", "/dev/loop1"))

	err := client.CreatePhysicalVolume(t.Context(), "/dev/loop0")

	require.NoError(t, err)
	assert.Equal(t, []string{
		"pvcreate",
		"--devices", "/dev/loop0,/dev/loop1",
		"/dev/loop0",
	}, fake.calls[0].Args())
}

func TestListPhysicalVolumesBuildsCommandAndParses(t *testing.T) {
	fake := &fakeRunner{output: []byte(`{
		"report": [{"pv": [
			{"pv_name": "/dev/loop0", "vg_name": "", "pv_size": "1073741824",
			 "pv_free": "1073741824", "pv_attr": "---", "pv_tags": ""},
			{"pv_name": "/dev/sda2", "vg_name": "vg0", "pv_size": "512110190592",
			 "pv_free": "10737418240", "pv_attr": "a--", "pv_tags": "fast,ssd"}
		]}], "log": []}`)}
	client := New(WithRunner(fake))

	pvs, err := client.ListPhysicalVolumes(t.Context())

	require.NoError(t, err)
	assert.Equal(t, []string{
		"pvs",
		"--reportformat", "json",
		"--units", "b",
		"--nosuffix",
		"-o", "pv_name,vg_name,pv_size,pv_free,pv_attr,pv_tags",
	}, fake.calls[0].Args())

	require.Len(t, pvs, 2)
	assert.Equal(t, PhysicalVolume{
		Device:      "/dev/loop0",
		VolumeGroup: "",
		SizeBytes:   1073741824,
		FreeBytes:   1073741824,
		Attributes:  "---",
		Tags:        nil,
	}, pvs[0])
	assert.Equal(t, []string{"fast", "ssd"}, pvs[1].Tags)
	assert.Equal(t, "vg0", pvs[1].VolumeGroup)
}

func TestListPhysicalVolumesRejectsMalformedSize(t *testing.T) {
	fake := &fakeRunner{output: []byte(`{
		"report": [{"pv": [
			{"pv_name": "/dev/loop0", "pv_size": "a lot", "pv_free": "0"}
		]}]}`)}
	client := New(WithRunner(fake))

	_, err := client.ListPhysicalVolumes(t.Context())

	assert.ErrorContains(t, err, "parsing size")
}

func TestRemovePhysicalVolumeBuildsCommand(t *testing.T) {
	fake := &fakeRunner{}
	client := New(WithRunner(fake))

	err := client.RemovePhysicalVolume(t.Context(), "/dev/loop0")

	require.NoError(t, err)
	assert.Equal(t, []string{"pvremove", "/dev/loop0"}, fake.calls[0].Args())
}

func TestRunnerErrorsPropagate(t *testing.T) {
	fake := &fakeRunner{err: assert.AnError}
	client := New(WithRunner(fake))

	err := client.CreatePhysicalVolume(t.Context(), "/dev/loop0")

	assert.ErrorIs(t, err, assert.AnError)
}
