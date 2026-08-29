//go:build integration

package lvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	runner "github.com/The127/go-runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sudoRunner elevates commands so the test process itself can stay
// unprivileged. It rewrites the command through the real runner, so
// RunError semantics and with them error classification are preserved.
type sudoRunner struct{}

var realRunner = runner.New()

func (sudoRunner) Run(ctx context.Context, cmd *runner.Cmd) ([]byte, error) {
	elevated := runner.Command("sudo", append([]string{"-n", cmd.Name()}, cmd.Args()...)...)
	return realRunner.Run(ctx, elevated)
}

func sudoRun(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n"}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sudo %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}

	return output, nil
}

// loopDevice creates a loop device over a sparse file and removes it
// again after the test.
func loopDevice(t *testing.T) Device {
	t.Helper()

	ctx := t.Context()

	_, err := sudoRun(ctx, "true")
	if err != nil {
		t.Skip("passwordless sudo unavailable, skipping lvm integration test")
	}

	backing := filepath.Join(t.TempDir(), "backing.img")
	require.NoError(t, os.WriteFile(backing, nil, 0o600))
	require.NoError(t, os.Truncate(backing, 1<<30))

	loopOut, err := sudoRun(ctx, "losetup", "--find", "--show", backing)
	require.NoError(t, err)
	loop := Device(strings.TrimSpace(string(loopOut)))
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_, _ = sudoRun(context.Background(), "losetup", "-d", string(loop))
	})

	return loop
}

func TestIntegrationPhysicalVolumeLifecycle(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_ = client.RemovePhysicalVolume(context.Background(), loop, RemovePhysicalVolumeOptions{})
	})

	pvs, err := client.ListPhysicalVolumes(ctx, ListPhysicalVolumesOptions{})
	require.NoError(t, err)
	require.Len(t, pvs, 1)
	assert.Equal(t, loop, pvs[0].Device)
	assert.Equal(t, "", pvs[0].VolumeGroup)
	assert.NotZero(t, pvs[0].SizeBytes)
	assert.Empty(t, pvs[0].Tags)

	require.NoError(t, client.RemovePhysicalVolume(ctx, loop, RemovePhysicalVolumeOptions{}))

	pvs, err = client.ListPhysicalVolumes(ctx, ListPhysicalVolumesOptions{})
	require.NoError(t, err)
	assert.Empty(t, pvs)
}

func TestIntegrationCreateOnMissingDeviceFails(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))

	err := client.CreatePhysicalVolume(t.Context(), "/dev/beanstore-absent", CreatePhysicalVolumeOptions{})

	assert.Error(t, err)
}

// vgFor wraps the loop devices in a vg through raw commands until the
// vg family is wrapped.
func vgFor(t *testing.T, loops ...Device) string {
	t.Helper()

	paths := make([]string, len(loops))
	for i, loop := range loops {
		paths[i] = string(loop)
	}
	devices := strings.Join(paths, ",")
	vg := fmt.Sprintf("beanstore-test-%d", os.Getpid())

	args := append([]string{"lvm", "vgcreate", "--devices", devices, vg}, paths...)
	_, err := sudoRun(t.Context(), args...)
	require.NoError(t, err)
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_, _ = sudoRun(context.Background(), "lvm", "vgremove", "--devices", devices, "-f", vg)
	})

	return vg
}

func TestIntegrationPhysicalVolumeTags(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	vgFor(t, loop)

	require.NoError(t, client.ChangePhysicalVolume(ctx, loop, ChangePhysicalVolumeOptions{AddTags: []string{"fast"}}))
	require.NoError(t, client.ChangePhysicalVolume(ctx, loop, ChangePhysicalVolumeOptions{AddTags: []string{"ssd"}}))

	pvs, err := client.ListPhysicalVolumes(ctx, ListPhysicalVolumesOptions{})
	require.NoError(t, err)
	require.Len(t, pvs, 1)
	assert.ElementsMatch(t, []string{"fast", "ssd"}, pvs[0].Tags)

	require.NoError(t, client.ChangePhysicalVolume(ctx, loop, ChangePhysicalVolumeOptions{RemoveTags: []string{"fast"}}))

	pvs, err = client.ListPhysicalVolumes(ctx, ListPhysicalVolumesOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"ssd"}, pvs[0].Tags)
}

func TestIntegrationPhysicalVolumeAllocatable(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	vgFor(t, loop)

	require.NoError(t, client.ChangePhysicalVolume(ctx, loop, ChangePhysicalVolumeOptions{Allocatable: Bool(false)}))

	assert.False(t, pvByDevice(t, client, loop).Allocatable)

	require.NoError(t, client.ChangePhysicalVolume(ctx, loop, ChangePhysicalVolumeOptions{Allocatable: Bool(true)}))

	assert.True(t, pvByDevice(t, client, loop).Allocatable)
}

func TestIntegrationPhysicalVolumeResize(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))

	pvs, err := client.ListPhysicalVolumes(ctx, ListPhysicalVolumesOptions{})
	require.NoError(t, err)
	sizeBefore := pvs[0].SizeBytes

	backing := backingFileOf(t, loop)
	require.NoError(t, os.Truncate(backing, 2<<30))
	_, err = sudoRun(ctx, "losetup", "-c", string(loop))
	require.NoError(t, err)

	require.NoError(t, client.ResizePhysicalVolume(ctx, loop, ResizePhysicalVolumeOptions{}))

	pvs, err = client.ListPhysicalVolumes(ctx, ListPhysicalVolumesOptions{})
	require.NoError(t, err)
	assert.Greater(t, pvs[0].SizeBytes, sizeBefore)
}

func backingFileOf(t *testing.T, loop Device) string {
	t.Helper()

	output, err := sudoRun(t.Context(), "losetup", "--noheadings", "--output", "BACK-FILE", string(loop))
	require.NoError(t, err)
	return strings.TrimSpace(string(output))
}

func TestIntegrationRegenerateUUIDChangesUUID(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	vgFor(t, loop)

	pvs, err := client.ListPhysicalVolumes(ctx, ListPhysicalVolumesOptions{})
	require.NoError(t, err)
	before := pvs[0].UUID
	require.NotEmpty(t, before)

	require.NoError(t, client.ChangePhysicalVolume(ctx, loop, ChangePhysicalVolumeOptions{RegenerateUUID: true}))

	pvs, err = client.ListPhysicalVolumes(ctx, ListPhysicalVolumesOptions{})
	require.NoError(t, err)
	assert.NotEqual(t, before, pvs[0].UUID)
}

func TestIntegrationMetadataIgnoreRoundTrip(t *testing.T) {
	// lvm refuses to disable the last metadata area of a vg, so the vg
	// needs a second pv keeping one
	first := loopDevice(t)
	second := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(first, second))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, first, CreatePhysicalVolumeOptions{}))
	require.NoError(t, client.CreatePhysicalVolume(ctx, second, CreatePhysicalVolumeOptions{}))
	vgFor(t, first, second)

	require.NoError(t, client.ChangePhysicalVolume(ctx, first, ChangePhysicalVolumeOptions{MetadataIgnore: Bool(true)}))

	assert.Zero(t, pvByDevice(t, client, first).UsedMetadataAreas)

	require.NoError(t, client.ChangePhysicalVolume(ctx, first, ChangePhysicalVolumeOptions{MetadataIgnore: Bool(false)}))

	assert.Equal(t, uint64(1), pvByDevice(t, client, first).UsedMetadataAreas)
}

func pvByDevice(t *testing.T, client *Client, device Device) PhysicalVolume {
	t.Helper()

	pvs, err := client.ListPhysicalVolumes(t.Context(), ListPhysicalVolumesOptions{})
	require.NoError(t, err)
	for _, pv := range pvs {
		if pv.Device == device {
			return pv
		}
	}

	t.Fatalf("pv %s not found", device)
	return PhysicalVolume{}
}

func TestIntegrationResizeToShrinksOrphanPV(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))

	require.NoError(t, client.ResizePhysicalVolume(ctx, loop, ResizePhysicalVolumeOptions{SizeBytes: 512 << 20}))

	pvs, err := client.ListPhysicalVolumes(ctx, ListPhysicalVolumesOptions{})
	require.NoError(t, err)
	assert.LessOrEqual(t, pvs[0].SizeBytes, uint64(512<<20))
}

func TestIntegrationSelectTargetsMatchingPVs(t *testing.T) {
	first := loopDevice(t)
	second := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(first, second))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, first, CreatePhysicalVolumeOptions{}))
	require.NoError(t, client.CreatePhysicalVolume(ctx, second, CreatePhysicalVolumeOptions{}))
	vgFor(t, first, second)

	require.NoError(t, client.ChangePhysicalVolume(ctx, first, ChangePhysicalVolumeOptions{
		AddTags: []string{"fast"},
	}))

	require.NoError(t, client.ChangePhysicalVolume(ctx, Select("pv_tags = {fast}"), ChangePhysicalVolumeOptions{
		AddTags: []string{"chosen"},
	}))

	pvs, err := client.ListPhysicalVolumes(ctx, ListPhysicalVolumesOptions{Select: "pv_tags = {chosen}"})
	require.NoError(t, err)
	require.Len(t, pvs, 1)
	assert.Equal(t, first, pvs[0].Device)

	require.NoError(t, client.ChangePhysicalVolume(ctx, All, ChangePhysicalVolumeOptions{
		AddTags: []string{"everywhere"},
	}))

	pvs, err = client.ListPhysicalVolumes(ctx, ListPhysicalVolumesOptions{Select: "pv_tags = {everywhere}"})
	require.NoError(t, err)
	assert.Len(t, pvs, 2)
}

func TestIntegrationErrorClassification(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	vgFor(t, loop)

	err := client.ChangePhysicalVolume(ctx, Device("/dev/beanstore-absent"), ChangePhysicalVolumeOptions{
		AddTags: []string{"x"},
	})
	assert.ErrorIs(t, err, ErrNotFound, "changing an absent pv")

	err = client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{})
	assert.ErrorIs(t, err, ErrInUse, "creating a pv over a vg member")

	err = client.RemovePhysicalVolume(ctx, loop, RemovePhysicalVolumeOptions{})
	assert.ErrorIs(t, err, ErrInUse, "removing a vg member")
}
