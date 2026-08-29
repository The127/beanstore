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
// unprivileged.
type sudoRunner struct{}

func (sudoRunner) Run(ctx context.Context, cmd *runner.Cmd) ([]byte, error) {
	return sudoRun(ctx, append([]string{cmd.Name()}, cmd.Args()...)...)
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
func loopDevice(t *testing.T) string {
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
	loop := strings.TrimSpace(string(loopOut))
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_, _ = sudoRun(context.Background(), "losetup", "-d", loop)
	})

	return loop
}

func TestIntegrationPhysicalVolumeLifecycle(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop))
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_ = client.RemovePhysicalVolume(context.Background(), loop)
	})

	pvs, err := client.ListPhysicalVolumes(ctx)
	require.NoError(t, err)
	require.Len(t, pvs, 1)
	assert.Equal(t, loop, pvs[0].Device)
	assert.Equal(t, "", pvs[0].VolumeGroup)
	assert.NotZero(t, pvs[0].SizeBytes)
	assert.Empty(t, pvs[0].Tags)

	require.NoError(t, client.RemovePhysicalVolume(ctx, loop))

	pvs, err = client.ListPhysicalVolumes(ctx)
	require.NoError(t, err)
	assert.Empty(t, pvs)
}

func TestIntegrationCreateOnMissingDeviceFails(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))

	err := client.CreatePhysicalVolume(t.Context(), "/dev/beanstore-absent")

	assert.Error(t, err)
}

// vgFor wraps the loop device in a vg through raw commands until the vg
// family is wrapped.
func vgFor(t *testing.T, loop string) string {
	t.Helper()

	vg := fmt.Sprintf("beanstore-test-%d", os.Getpid())
	_, err := sudoRun(t.Context(), "lvm", "vgcreate", "--devices", loop, vg, loop)
	require.NoError(t, err)
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_, _ = sudoRun(context.Background(), "lvm", "vgremove", "--devices", loop, "-f", vg)
	})

	return vg
}

func TestIntegrationPhysicalVolumeTags(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop))
	vgFor(t, loop)

	require.NoError(t, client.AddPhysicalVolumeTag(ctx, loop, "fast"))
	require.NoError(t, client.AddPhysicalVolumeTag(ctx, loop, "ssd"))

	pvs, err := client.ListPhysicalVolumes(ctx)
	require.NoError(t, err)
	require.Len(t, pvs, 1)
	assert.ElementsMatch(t, []string{"fast", "ssd"}, pvs[0].Tags)

	require.NoError(t, client.RemovePhysicalVolumeTag(ctx, loop, "fast"))

	pvs, err = client.ListPhysicalVolumes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ssd"}, pvs[0].Tags)
}

func TestIntegrationPhysicalVolumeAllocatable(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop))
	vgFor(t, loop)

	require.NoError(t, client.SetPhysicalVolumeAllocatable(ctx, loop, false))

	pvs, err := client.ListPhysicalVolumes(ctx)
	require.NoError(t, err)
	assert.NotEqual(t, "a", pvs[0].Attributes[:1])

	require.NoError(t, client.SetPhysicalVolumeAllocatable(ctx, loop, true))

	pvs, err = client.ListPhysicalVolumes(ctx)
	require.NoError(t, err)
	assert.Equal(t, "a", pvs[0].Attributes[:1])
}

func TestIntegrationPhysicalVolumeResize(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop))

	pvs, err := client.ListPhysicalVolumes(ctx)
	require.NoError(t, err)
	sizeBefore := pvs[0].SizeBytes

	backing := backingFileOf(t, loop)
	require.NoError(t, os.Truncate(backing, 2<<30))
	_, err = sudoRun(ctx, "losetup", "-c", loop)
	require.NoError(t, err)

	require.NoError(t, client.ResizePhysicalVolume(ctx, loop))

	pvs, err = client.ListPhysicalVolumes(ctx)
	require.NoError(t, err)
	assert.Greater(t, pvs[0].SizeBytes, sizeBefore)
}

func backingFileOf(t *testing.T, loop string) string {
	t.Helper()

	output, err := sudoRun(t.Context(), "losetup", "--noheadings", "--output", "BACK-FILE", loop)
	require.NoError(t, err)
	return strings.TrimSpace(string(output))
}
