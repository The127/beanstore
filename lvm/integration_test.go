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

// vgFor wraps the loop devices in a vg through the library.
func vgFor(t *testing.T, loops ...Device) string {
	t.Helper()

	client := New(WithRunner(sudoRunner{}), WithDevices(loops...))
	vg := fmt.Sprintf("beanstore-test-%d", os.Getpid())

	require.NoError(t, client.CreateVolumeGroup(t.Context(), vg, loops, CreateVolumeGroupOptions{}))
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_ = client.RemoveVolumeGroup(context.Background(), vg, RemoveVolumeGroupOptions{Force: true})
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

func TestIntegrationDisplayPhysicalVolume(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	vgFor(t, loop)

	output, err := client.DisplayPhysicalVolume(ctx, loop, DisplayPhysicalVolumeOptions{})
	require.NoError(t, err)
	assert.Contains(t, output, string(loop))

	output, err = client.DisplayPhysicalVolume(ctx, loop, DisplayPhysicalVolumeOptions{Maps: true})
	require.NoError(t, err)
	assert.Contains(t, output, string(loop))
}

func TestIntegrationScanPhysicalVolumes(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	vgFor(t, loop)

	require.NoError(t, client.ScanPhysicalVolumes(ctx, ScanPhysicalVolumesOptions{Device: loop}))
	require.NoError(t, client.ScanPhysicalVolumes(ctx, ScanPhysicalVolumesOptions{
		Device:       loop,
		Autoactivate: true,
	}))
}

func TestIntegrationCheckAndDumpPhysicalVolume(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	vgFor(t, loop)

	require.NoError(t, client.CheckPhysicalVolume(ctx, loop, CheckPhysicalVolumeOptions{}))

	// lvm 2.03.16 rejects --devices scoped dumps, disable scoping
	output, err := client.DumpPhysicalVolume(ctx, loop, DumpHeaders, DumpPhysicalVolumeOptions{
		CommonOptions: CommonOptions{Devices: []Device{}},
	})
	require.NoError(t, err)
	assert.Contains(t, output, "label_header")
}

func TestIntegrationVolumeGroupLifecycle(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))

	name := fmt.Sprintf("beanstore-vgtest-%d", os.Getpid())
	require.NoError(t, client.CreateVolumeGroup(ctx, name, []Device{loop}, CreateVolumeGroupOptions{
		AddTags: []string{"beanstore-test"},
	}))
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_ = client.RemoveVolumeGroup(context.Background(), name, RemoveVolumeGroupOptions{Force: true})
	})

	vgs, err := client.ListVolumeGroups(ctx, ListVolumeGroupsOptions{Select: "vg_tags = {beanstore-test}"})
	require.NoError(t, err)
	require.Len(t, vgs, 1)
	assert.Equal(t, name, vgs[0].Name)
	assert.Equal(t, uint64(1), vgs[0].PVCount)
	assert.Zero(t, vgs[0].LVCount)
	assert.NotZero(t, vgs[0].SizeBytes)
	assert.NotZero(t, vgs[0].ExtentSizeBytes)
	assert.Equal(t, vgs[0].SizeBytes, vgs[0].FreeBytes)

	err = client.CreateVolumeGroup(ctx, name, []Device{loop}, CreateVolumeGroupOptions{})
	assert.ErrorIs(t, err, ErrAlreadyExists, "duplicate vg name")

	require.NoError(t, client.RemoveVolumeGroup(ctx, name, RemoveVolumeGroupOptions{}))

	vgs, err = client.ListVolumeGroups(ctx, ListVolumeGroupsOptions{Select: "vg_tags = {beanstore-test}"})
	require.NoError(t, err)
	assert.Empty(t, vgs)
}

func TestIntegrationVolumeGroupMembership(t *testing.T) {
	first := loopDevice(t)
	second := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(first, second))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, first, CreatePhysicalVolumeOptions{}))
	name := vgFor(t, first)

	require.NoError(t, client.ExtendVolumeGroup(ctx, name, []Device{second}, ExtendVolumeGroupOptions{}))

	vgs, err := client.ListVolumeGroups(ctx, ListVolumeGroupsOptions{})
	require.NoError(t, err)
	require.Len(t, vgs, 1)
	assert.Equal(t, uint64(2), vgs[0].PVCount)

	require.NoError(t, client.ReduceVolumeGroup(ctx, name, []Device{second}, ReduceVolumeGroupOptions{}))

	vgs, err = client.ListVolumeGroups(ctx, ListVolumeGroupsOptions{})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), vgs[0].PVCount)
}

func TestIntegrationVolumeGroupRename(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	name := vgFor(t, loop)
	renamed := name + "-renamed"
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_ = client.RemoveVolumeGroup(context.Background(), renamed, RemoveVolumeGroupOptions{Force: true})
	})

	require.NoError(t, client.RenameVolumeGroup(ctx, name, renamed, RenameVolumeGroupOptions{}))

	vgs, err := client.ListVolumeGroups(ctx, ListVolumeGroupsOptions{})
	require.NoError(t, err)
	require.Len(t, vgs, 1)
	assert.Equal(t, renamed, vgs[0].Name)

	require.NoError(t, client.RenameVolumeGroup(ctx, renamed, name, RenameVolumeGroupOptions{}))
}

func TestIntegrationVolumeGroupChangeAndSelect(t *testing.T) {
	loop := loopDevice(t)
	spare := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop, spare))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	require.NoError(t, client.CreatePhysicalVolume(ctx, spare, CreatePhysicalVolumeOptions{}))
	name := vgFor(t, loop)

	require.NoError(t, client.ChangeVolumeGroup(ctx, Name(name), ChangeVolumeGroupOptions{
		AddTags: []string{"retiring"},
	}))

	require.NoError(t, client.ChangeVolumeGroup(ctx, Select("vg_tags = {retiring}"), ChangeVolumeGroupOptions{
		AddTags:    []string{"drained"},
		RemoveTags: []string{"retiring"},
	}))

	vgs, err := client.ListVolumeGroups(ctx, ListVolumeGroupsOptions{Select: "vg_tags = {drained}"})
	require.NoError(t, err)
	require.Len(t, vgs, 1)
	assert.Equal(t, name, vgs[0].Name)

	require.NoError(t, client.ChangeVolumeGroup(ctx, Name(name), ChangeVolumeGroupOptions{
		Resizeable: Bool(false),
	}))
	err = client.ExtendVolumeGroup(ctx, name, []Device{loop}, ExtendVolumeGroupOptions{})
	assert.ErrorIs(t, err, ErrAlreadyExists, "extending with an existing member")

	err = client.ExtendVolumeGroup(ctx, name, []Device{spare}, ExtendVolumeGroupOptions{})
	assert.ErrorIs(t, err, ErrNotAllowed, "extending a non resizeable vg")
	require.NoError(t, client.ChangeVolumeGroup(ctx, Name(name), ChangeVolumeGroupOptions{
		Resizeable: Bool(true),
	}))
}

func TestIntegrationVolumeGroupActivationCheckDisplayScan(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	name := vgFor(t, loop)

	require.NoError(t, client.DeactivateVolumeGroup(ctx, Name(name), DeactivateVolumeGroupOptions{}))
	require.NoError(t, client.ActivateVolumeGroup(ctx, Name(name), ActivateVolumeGroupOptions{}))

	require.NoError(t, client.CheckVolumeGroup(ctx, name, CheckVolumeGroupOptions{}))

	output, err := client.DisplayVolumeGroup(ctx, name, DisplayVolumeGroupOptions{})
	require.NoError(t, err)
	assert.Contains(t, output, name)

	require.NoError(t, client.ScanVolumeGroups(ctx, ScanVolumeGroupsOptions{}))
}

func TestIntegrationMetadataBackupRestore(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	name := vgFor(t, loop)

	backup := filepath.Join(t.TempDir(), "vg.backup")
	require.NoError(t, os.Chmod(filepath.Dir(backup), 0o755))
	require.NoError(t, client.BackupVolumeGroupMetadata(ctx, name, BackupVolumeGroupMetadataOptions{File: backup}))

	require.NoError(t, client.ChangeVolumeGroup(ctx, Name(name), ChangeVolumeGroupOptions{
		AddTags: []string{"after-backup"},
	}))

	require.NoError(t, client.DeactivateVolumeGroup(ctx, Name(name), DeactivateVolumeGroupOptions{}))
	require.NoError(t, client.RestoreVolumeGroupMetadata(ctx, name, RestoreVolumeGroupMetadataOptions{File: backup}))

	vgs, err := client.ListVolumeGroups(ctx, ListVolumeGroupsOptions{})
	require.NoError(t, err)
	require.Len(t, vgs, 1)
	assert.Empty(t, vgs[0].Tags, "restore must roll back the tag added after the backup")
}

func TestIntegrationExportImportRoundTrip(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	name := vgFor(t, loop)

	require.NoError(t, client.DeactivateVolumeGroup(ctx, Name(name), DeactivateVolumeGroupOptions{}))
	require.NoError(t, client.ExportVolumeGroup(ctx, Name(name), ExportVolumeGroupOptions{}))

	vgs, err := client.ListVolumeGroups(ctx, ListVolumeGroupsOptions{})
	require.NoError(t, err)
	require.Len(t, vgs, 1)
	assert.True(t, vgs[0].Exported)

	require.NoError(t, client.ImportVolumeGroup(ctx, Name(name), ImportVolumeGroupOptions{}))

	vgs, err = client.ListVolumeGroups(ctx, ListVolumeGroupsOptions{})
	require.NoError(t, err)
	assert.False(t, vgs[0].Exported)
}

func TestIntegrationMergeAndSplit(t *testing.T) {
	first := loopDevice(t)
	second := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(first, second))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, first, CreatePhysicalVolumeOptions{}))
	require.NoError(t, client.CreatePhysicalVolume(ctx, second, CreatePhysicalVolumeOptions{}))

	name := vgFor(t, first)
	other := name + "-b"
	require.NoError(t, client.CreateVolumeGroup(ctx, other, []Device{second}, CreateVolumeGroupOptions{}))
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is done during cleanup
		_ = client.RemoveVolumeGroup(context.Background(), other, RemoveVolumeGroupOptions{Force: true})
	})

	require.NoError(t, client.DeactivateVolumeGroup(ctx, Name(other), DeactivateVolumeGroupOptions{}))
	require.NoError(t, client.MergeVolumeGroups(ctx, name, other, MergeVolumeGroupsOptions{}))

	vgs, err := client.ListVolumeGroups(ctx, ListVolumeGroupsOptions{})
	require.NoError(t, err)
	require.Len(t, vgs, 1)
	assert.Equal(t, uint64(2), vgs[0].PVCount)

	require.NoError(t, client.SplitVolumeGroup(ctx, name, other, []Device{second}, SplitVolumeGroupOptions{}))

	vgs, err = client.ListVolumeGroups(ctx, ListVolumeGroupsOptions{})
	require.NoError(t, err)
	assert.Len(t, vgs, 2)

	require.NoError(t, client.MakeVolumeGroupNodes(ctx, name, MakeVolumeGroupNodesOptions{}))
}

func TestIntegrationThinVolumeLifecycle(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	vg := vgFor(t, loop)

	require.NoError(t, client.CreateThinPool(ctx, vg, "pool0", 512<<20, CreateThinPoolOptions{}))
	require.NoError(t, client.CreateThinVolume(ctx, vg, "pool0", "vol1", 64<<20, CreateThinVolumeOptions{
		AddTags:  []string{"state.creating"},
		Activate: Bool(false),
	}))

	lvs, err := client.ListLogicalVolumes(ctx, ListLogicalVolumesOptions{
		VG:     vg,
		Select: "lv_tags = {state.creating}",
	})
	require.NoError(t, err)
	require.Len(t, lvs, 1)
	assert.Equal(t, "vol1", lvs[0].Name)
	assert.Equal(t, uint64(64<<20), lvs[0].SizeBytes)
	assert.Equal(t, "pool0", lvs[0].Pool)
	assert.False(t, lvs[0].Active, "created inactive")
	assert.Contains(t, lvs[0].Layout, "thin")

	require.NoError(t, client.ActivateLogicalVolume(ctx, Name(vg+"/vol1"), ActivateLogicalVolumeOptions{}))

	lvs, err = client.ListLogicalVolumes(ctx, ListLogicalVolumesOptions{VG: vg, Select: "lv_name = vol1"})
	require.NoError(t, err)
	require.Len(t, lvs, 1)
	assert.True(t, lvs[0].Active)
	assert.NotEmpty(t, lvs[0].Path)

	require.NoError(t, client.ChangeLogicalVolume(ctx, Name(vg+"/vol1"), ChangeLogicalVolumeOptions{
		AddTags:    []string{"state.ready"},
		RemoveTags: []string{"state.creating"},
	}))
	tagged, err := client.ListLogicalVolumes(ctx, ListLogicalVolumesOptions{VG: vg, Select: "lv_tags = {state.ready}"})
	require.NoError(t, err)
	require.Len(t, tagged, 1)
	assert.Equal(t, []string{"state.ready"}, tagged[0].Tags)

	require.NoError(t, client.DeactivateLogicalVolume(ctx, Name(vg+"/vol1"), DeactivateLogicalVolumeOptions{}))
	lvs, err = client.ListLogicalVolumes(ctx, ListLogicalVolumesOptions{VG: vg, Select: "lv_name = vol1"})
	require.NoError(t, err)
	assert.False(t, lvs[0].Active)

	pools, err := client.ListLogicalVolumes(ctx, ListLogicalVolumesOptions{VG: vg, Select: "lv_name = pool0"})
	require.NoError(t, err)
	require.Len(t, pools, 1)
	assert.Contains(t, pools[0].Layout, "pool")

	require.NoError(t, client.RemoveLogicalVolume(ctx, Name(vg+"/vol1"), RemoveLogicalVolumeOptions{Force: true}))

	lvs, err = client.ListLogicalVolumes(ctx, ListLogicalVolumesOptions{VG: vg, Select: "lv_name = vol1"})
	require.NoError(t, err)
	assert.Empty(t, lvs)
}

func TestIntegrationLinearVolumeLifecycle(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	vg := vgFor(t, loop)

	require.NoError(t, client.CreateLogicalVolume(ctx, vg, "lv0", 32<<20, CreateLogicalVolumeOptions{}))

	lvs, err := client.ListLogicalVolumes(ctx, ListLogicalVolumesOptions{VG: vg})
	require.NoError(t, err)
	require.Len(t, lvs, 1)
	assert.Equal(t, uint64(32<<20), lvs[0].SizeBytes)
	assert.Contains(t, lvs[0].Layout, "linear")

	require.NoError(t, client.RemoveLogicalVolume(ctx, Name(vg+"/lv0"), RemoveLogicalVolumeOptions{Force: true}))
}

func TestIntegrationLogicalVolumeResizeAndRename(t *testing.T) {
	loop := loopDevice(t)
	client := New(WithRunner(sudoRunner{}), WithDevices(loop))
	ctx := t.Context()

	require.NoError(t, client.CreatePhysicalVolume(ctx, loop, CreatePhysicalVolumeOptions{}))
	vg := vgFor(t, loop)

	require.NoError(t, client.CreateThinPool(ctx, vg, "pool0", 256<<20, CreateThinPoolOptions{}))
	require.NoError(t, client.CreateThinVolume(ctx, vg, "pool0", "vol1", 64<<20, CreateThinVolumeOptions{}))

	require.NoError(t, client.ExtendLogicalVolume(ctx, vg+"/vol1", 32<<20, ExtendLogicalVolumeOptions{Relative: true}))
	assert.Equal(t, uint64(96<<20), lvByName(t, client, vg, "vol1").SizeBytes)

	require.NoError(t, client.ResizeLogicalVolume(ctx, vg+"/vol1", 128<<20, ResizeLogicalVolumeOptions{}))
	assert.Equal(t, uint64(128<<20), lvByName(t, client, vg, "vol1").SizeBytes)

	require.NoError(t, client.ReduceLogicalVolume(ctx, vg+"/vol1", 64<<20, ReduceLogicalVolumeOptions{}))
	assert.Equal(t, uint64(64<<20), lvByName(t, client, vg, "vol1").SizeBytes)

	require.NoError(t, client.RenameLogicalVolume(ctx, vg, "vol1", "vol2", RenameLogicalVolumeOptions{}))
	assert.Equal(t, uint64(64<<20), lvByName(t, client, vg, "vol2").SizeBytes)
}

func lvByName(t *testing.T, client *Client, vg, name string) LogicalVolume {
	t.Helper()

	lvs, err := client.ListLogicalVolumes(t.Context(), ListLogicalVolumesOptions{
		VG:     vg,
		Select: Select("lv_name = " + name),
	})
	require.NoError(t, err)
	require.Len(t, lvs, 1)
	return lvs[0]
}
