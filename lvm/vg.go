package lvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// VolumeGroup is one vg as reported by vgs.
type VolumeGroup struct {
	Name            string
	UUID            string
	SizeBytes       uint64
	FreeBytes       uint64
	ExtentSizeBytes uint64
	Extents         uint64
	FreeExtents     uint64
	PVCount         uint64
	LVCount         uint64
	SnapshotCount   uint64
	MissingPVCount  uint64
	Tags            []string
	Attributes      string
	Exported        bool
	Partial         bool
	Shared          bool
	Autoactivation  bool
}

// CreateVolumeGroupOptions configures CreateVolumeGroup.
type CreateVolumeGroupOptions struct {
	CommonOptions
	AddTags []string
	// ExtentSizeBytes sets the physical extent size, lvm's default when
	// zero.
	ExtentSizeBytes uint64
	// SetAutoactivation controls whether the vg autoactivates on boot
	// and device appearance.
	SetAutoactivation *bool
	// Force initializes member pvs without confirmation, wiping
	// recognizable signatures on the devices.
	Force bool
}

// CreateVolumeGroup creates a vg on the given devices, initializing
// them as pvs where needed.
func (c *Client) CreateVolumeGroup(ctx context.Context, name string, devices []Device, opts CreateVolumeGroupOptions) error {
	cmd := c.metadataCommand("vgcreate", opts.CommonOptions)
	for _, tag := range opts.AddTags {
		cmd = cmd.Append("--addtag", tag)
	}
	if opts.ExtentSizeBytes > 0 {
		cmd = cmd.Append("-s", strconv.FormatUint(opts.ExtentSizeBytes, 10)+"b")
	}
	if opts.SetAutoactivation != nil {
		cmd = cmd.Append("--setautoactivation", flagValue(*opts.SetAutoactivation))
	}
	if opts.Force {
		cmd = cmd.Append("-f")
	}
	cmd = cmd.Append(name)
	for _, device := range devices {
		cmd = cmd.Append(string(device))
	}

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("creating volume group %s: %w", name, err)
	}

	return nil
}

// ListVolumeGroupsOptions configures ListVolumeGroups.
type ListVolumeGroupsOptions struct {
	CommonOptions
	// Select filters the report by lvm selection criteria.
	Select Select
}

// ListVolumeGroups reports all vgs visible to the client.
func (c *Client) ListVolumeGroups(ctx context.Context, opts ListVolumeGroupsOptions) ([]VolumeGroup, error) {
	if opts.Autobackup != nil {
		return nil, errAutobackupNotSupported
	}

	cmd := c.command("vgs", opts.CommonOptions).Append(
		"--reportformat", "json",
		"--units", "b",
		"--nosuffix",
		"--binary",
		"-o", "vg_name,vg_uuid,vg_size,vg_free,vg_extent_size,vg_extent_count,"+
			"vg_free_count,pv_count,lv_count,snap_count,vg_missing_pv_count,"+
			"vg_tags,vg_attr,vg_exported,vg_partial,vg_shared,vg_autoactivation",
	)
	if opts.Select != "" {
		cmd = cmd.Append("-S", string(opts.Select))
	}

	output, err := c.run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("listing volume groups: %w", err)
	}

	return parseVGReport(output)
}

// RemoveVolumeGroupOptions configures RemoveVolumeGroup.
type RemoveVolumeGroupOptions struct {
	CommonOptions
	// Force removes contained lvs without per lv confirmation.
	Force bool
}

// RemoveVolumeGroup removes the given vg.
func (c *Client) RemoveVolumeGroup(ctx context.Context, name string, opts RemoveVolumeGroupOptions) error {
	if opts.Autobackup != nil {
		return errAutobackupNotSupported
	}

	cmd := c.command("vgremove", opts.CommonOptions)
	if opts.Force {
		cmd = cmd.Append("-f")
	}
	cmd = cmd.Append(name)

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("removing volume group %s: %w", name, err)
	}

	return nil
}

type vgReport struct {
	Report []struct {
		VG []struct {
			Name           string `json:"vg_name"`
			UUID           string `json:"vg_uuid"`
			Size           string `json:"vg_size"`
			Free           string `json:"vg_free"`
			ExtentSize     string `json:"vg_extent_size"`
			ExtentCount    string `json:"vg_extent_count"`
			FreeCount      string `json:"vg_free_count"`
			PVCount        string `json:"pv_count"`
			LVCount        string `json:"lv_count"`
			SnapCount      string `json:"snap_count"`
			MissingPVCount string `json:"vg_missing_pv_count"`
			Tags           string `json:"vg_tags"`
			Attr           string `json:"vg_attr"`
			Exported       string `json:"vg_exported"`
			Partial        string `json:"vg_partial"`
			Shared         string `json:"vg_shared"`
			Autoactivation string `json:"vg_autoactivation"`
		} `json:"vg"`
	} `json:"report"`
}

func parseVGReport(output []byte) ([]VolumeGroup, error) {
	var report vgReport
	err := json.Unmarshal(output, &report)
	if err != nil {
		return nil, fmt.Errorf("parsing vgs report: %w", err)
	}

	var groups []VolumeGroup
	for _, r := range report.Report {
		for _, vg := range r.VG {
			numbers := map[string]string{
				"size":              vg.Size,
				"free space":        vg.Free,
				"extent size":       vg.ExtentSize,
				"extent count":      vg.ExtentCount,
				"free extent count": vg.FreeCount,
				"pv count":          vg.PVCount,
				"lv count":          vg.LVCount,
				"snapshot count":    vg.SnapCount,
				"missing pv count":  vg.MissingPVCount,
			}
			parsed := make(map[string]uint64, len(numbers))
			for what, value := range numbers {
				parsed[what], err = strconv.ParseUint(value, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("parsing %s of %s: %w", what, vg.Name, err)
				}
			}

			// tags may not contain commas, splitting is unambiguous
			var tags []string
			if vg.Tags != "" {
				tags = strings.Split(vg.Tags, ",")
			}

			groups = append(groups, VolumeGroup{
				Name:            vg.Name,
				UUID:            vg.UUID,
				SizeBytes:       parsed["size"],
				FreeBytes:       parsed["free space"],
				ExtentSizeBytes: parsed["extent size"],
				Extents:         parsed["extent count"],
				FreeExtents:     parsed["free extent count"],
				PVCount:         parsed["pv count"],
				LVCount:         parsed["lv count"],
				SnapshotCount:   parsed["snapshot count"],
				MissingPVCount:  parsed["missing pv count"],
				Tags:            tags,
				Attributes:      vg.Attr,
				Exported:        vg.Exported == "1",
				Partial:         vg.Partial == "1",
				Shared:          vg.Shared == "1",
				Autoactivation:  vg.Autoactivation == "1",
			})
		}
	}

	return groups, nil
}

// ExtendVolumeGroupOptions configures ExtendVolumeGroup.
type ExtendVolumeGroupOptions struct {
	CommonOptions
	// Force initializes member pvs without confirmation, wiping
	// recognizable signatures on the devices.
	Force bool
	// RestoreMissing readds a pv that was missing and reappeared,
	// instead of adding it as a new pv.
	RestoreMissing bool
}

// ExtendVolumeGroup adds the given devices to the vg, initializing them
// as pvs where needed.
func (c *Client) ExtendVolumeGroup(ctx context.Context, name string, devices []Device, opts ExtendVolumeGroupOptions) error {
	cmd := c.metadataCommand("vgextend", opts.CommonOptions)
	if opts.Force {
		cmd = cmd.Append("-f")
	}
	if opts.RestoreMissing {
		cmd = cmd.Append("--restoremissing")
	}
	cmd = cmd.Append(name)
	for _, device := range devices {
		cmd = cmd.Append(string(device))
	}

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("extending volume group %s: %w", name, err)
	}

	return nil
}

// ReduceVolumeGroupOptions configures ReduceVolumeGroup.
type ReduceVolumeGroupOptions struct {
	CommonOptions
	// RemoveUnused removes all pvs without allocated extents instead of
	// named devices.
	RemoveUnused bool
	// RemoveMissing removes all missing pvs instead of named devices,
	// making a partial vg consistent again.
	RemoveMissing bool
	// Force also removes lvs affected by removed missing pvs.
	Force bool
}

// ReduceVolumeGroup removes pvs from the vg: the given devices, or per
// options all unused or all missing pvs.
func (c *Client) ReduceVolumeGroup(ctx context.Context, name string, devices []Device, opts ReduceVolumeGroupOptions) error {
	forms := 0
	if len(devices) > 0 {
		forms++
	}
	if opts.RemoveUnused {
		forms++
	}
	if opts.RemoveMissing {
		forms++
	}
	if forms != 1 {
		return errors.New("reducing a volume group requires exactly one of devices, RemoveUnused or RemoveMissing")
	}

	cmd := c.metadataCommand("vgreduce", opts.CommonOptions)
	if opts.RemoveUnused {
		cmd = cmd.Append("-a")
	}
	if opts.RemoveMissing {
		cmd = cmd.Append("--removemissing")
	}
	if opts.Force {
		cmd = cmd.Append("-f")
	}
	cmd = cmd.Append(name)
	for _, device := range devices {
		cmd = cmd.Append(string(device))
	}

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("reducing volume group %s: %w", name, err)
	}

	return nil
}

// RenameVolumeGroupOptions configures RenameVolumeGroup.
type RenameVolumeGroupOptions struct {
	CommonOptions
}

// RenameVolumeGroup renames the vg addressed by name or UUID.
func (c *Client) RenameVolumeGroup(ctx context.Context, oldName, newName string, opts RenameVolumeGroupOptions) error {
	cmd := c.metadataCommand("vgrename", opts.CommonOptions).Append(oldName, newName)

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("renaming volume group %s to %s: %w", oldName, newName, err)
	}

	return nil
}

// AllocationPolicy is a vg or lv extent allocation policy.
type AllocationPolicy string

// AllocationPolicy values.
const (
	AllocationContiguous  AllocationPolicy = "contiguous"
	AllocationCling       AllocationPolicy = "cling"
	AllocationClingByTags AllocationPolicy = "cling_by_tags"
	AllocationNormal      AllocationPolicy = "normal"
	AllocationAnywhere    AllocationPolicy = "anywhere"
	AllocationInherit     AllocationPolicy = "inherit"
)

// ChangeVolumeGroupOptions configures ChangeVolumeGroup. At least one
// property must be set.
type ChangeVolumeGroupOptions struct {
	CommonOptions
	AddTags    []string
	RemoveTags []string
	// Resizeable controls whether pvs may be added or removed.
	Resizeable *bool
	// MaxLogicalVolumes limits the lv count, zero means unlimited.
	MaxLogicalVolumes *uint64
	// MaxPhysicalVolumes limits the pv count, zero means unlimited.
	MaxPhysicalVolumes *uint64
	// ExtentSizeBytes changes the physical extent size.
	ExtentSizeBytes uint64
	// RegenerateUUID gives the vg a new random UUID.
	RegenerateUUID bool
	// SetAutoactivation controls whether the vg autoactivates on boot
	// and device appearance.
	SetAutoactivation *bool
	// Allocation sets the extent allocation policy.
	Allocation AllocationPolicy
}

// ChangeVolumeGroup changes properties of the vgs the target selects.
// All requested changes run as one lvm command.
func (c *Client) ChangeVolumeGroup(ctx context.Context, target Selector, opts ChangeVolumeGroupOptions) error {
	cmd := c.metadataCommand("vgchange", opts.CommonOptions)

	properties := 0
	for _, tag := range opts.AddTags {
		cmd = cmd.Append("--addtag", tag)
		properties++
	}
	for _, tag := range opts.RemoveTags {
		cmd = cmd.Append("--deltag", tag)
		properties++
	}
	if opts.Resizeable != nil {
		cmd = cmd.Append("-x", flagValue(*opts.Resizeable))
		properties++
	}
	if opts.MaxLogicalVolumes != nil {
		cmd = cmd.Append("-l", strconv.FormatUint(*opts.MaxLogicalVolumes, 10))
		properties++
	}
	if opts.MaxPhysicalVolumes != nil {
		cmd = cmd.Append("-p", strconv.FormatUint(*opts.MaxPhysicalVolumes, 10))
		properties++
	}
	if opts.ExtentSizeBytes > 0 {
		cmd = cmd.Append("-s", strconv.FormatUint(opts.ExtentSizeBytes, 10)+"b")
		properties++
	}
	if opts.RegenerateUUID {
		cmd = cmd.Append("-u")
		properties++
	}
	if opts.SetAutoactivation != nil {
		cmd = cmd.Append("--setautoactivation", flagValue(*opts.SetAutoactivation))
		properties++
	}
	if opts.Allocation != "" {
		cmd = cmd.Append("--alloc", string(opts.Allocation))
		properties++
	}

	if properties == 0 {
		return errors.New("changing a volume group requires at least one property")
	}

	cmd, err := appendSelector(cmd, target, "")
	if err != nil {
		return err
	}

	_, err = c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("changing volume groups %v: %w", target, err)
	}

	return nil
}

// ActivationMode is how activation treats vgs with missing pvs.
type ActivationMode string

// ActivationMode values.
const (
	ActivationPartial  ActivationMode = "partial"
	ActivationDegraded ActivationMode = "degraded"
	ActivationComplete ActivationMode = "complete"
)

// ActivateVolumeGroupOptions configures ActivateVolumeGroup.
type ActivateVolumeGroupOptions struct {
	CommonOptions
	// IgnoreActivationSkip also activates lvs flagged to be skipped.
	IgnoreActivationSkip bool
	// Mode is how to treat vgs with missing pvs, lvm's default when
	// empty.
	Mode ActivationMode
}

// ActivateVolumeGroup activates the lvs of the vgs the target selects.
func (c *Client) ActivateVolumeGroup(ctx context.Context, target Selector, opts ActivateVolumeGroupOptions) error {
	cmd := c.metadataCommand("vgchange", opts.CommonOptions).Append("-a", "y")
	if opts.IgnoreActivationSkip {
		cmd = cmd.Append("-K")
	}
	if opts.Mode != "" {
		cmd = cmd.Append("--activationmode", string(opts.Mode))
	}

	cmd, err := appendSelector(cmd, target, "")
	if err != nil {
		return err
	}

	_, err = c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("activating volume groups %v: %w", target, err)
	}

	return nil
}

// DeactivateVolumeGroupOptions configures DeactivateVolumeGroup.
type DeactivateVolumeGroupOptions struct {
	CommonOptions
}

// DeactivateVolumeGroup deactivates the lvs of the vgs the target
// selects.
func (c *Client) DeactivateVolumeGroup(ctx context.Context, target Selector, opts DeactivateVolumeGroupOptions) error {
	cmd := c.metadataCommand("vgchange", opts.CommonOptions).Append("-a", "n")

	cmd, err := appendSelector(cmd, target, "")
	if err != nil {
		return err
	}

	_, err = c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("deactivating volume groups %v: %w", target, err)
	}

	return nil
}

// CheckVolumeGroupOptions configures CheckVolumeGroup.
type CheckVolumeGroupOptions struct {
	CommonOptions
}

// CheckVolumeGroup checks the consistency of the given vg, all vgs when
// the name is empty. Findings go to lvm's log, the returned error is
// the verdict.
func (c *Client) CheckVolumeGroup(ctx context.Context, name string, opts CheckVolumeGroupOptions) error {
	if opts.Autobackup != nil {
		return errAutobackupNotSupported
	}

	cmd := c.command("vgck", opts.CommonOptions)
	if name != "" {
		cmd = cmd.Append(name)
	}

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("checking volume group %s: %w", name, err)
	}

	return nil
}

// DisplayVolumeGroupOptions configures DisplayVolumeGroup.
type DisplayVolumeGroupOptions struct {
	CommonOptions
	// Short prints a condensed one line summary per vg.
	Short bool
}

// DisplayVolumeGroup returns lvm's human readable description of the
// given vg, of all vgs when the name is empty. For machine readable
// data use ListVolumeGroups.
func (c *Client) DisplayVolumeGroup(ctx context.Context, name string, opts DisplayVolumeGroupOptions) (string, error) {
	if opts.Autobackup != nil {
		return "", errAutobackupNotSupported
	}

	cmd := c.command("vgdisplay", opts.CommonOptions)
	if opts.Short {
		cmd = cmd.Append("-s")
	}
	if name != "" {
		cmd = cmd.Append(name)
	}

	output, err := c.run(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("displaying volume group %s: %w", name, err)
	}

	return string(output), nil
}

// ScanVolumeGroupsOptions configures ScanVolumeGroups.
type ScanVolumeGroupsOptions struct {
	CommonOptions
	// MkNodes also recreates missing vg device nodes under /dev.
	MkNodes bool
}

// ScanVolumeGroups rescans all devices for vgs. Plain vg listing is
// ListVolumeGroups' job.
func (c *Client) ScanVolumeGroups(ctx context.Context, opts ScanVolumeGroupsOptions) error {
	if opts.Autobackup != nil {
		return errAutobackupNotSupported
	}

	cmd := c.command("vgscan", opts.CommonOptions)
	if opts.MkNodes {
		cmd = cmd.Append("--mknodes")
	}

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("scanning volume groups: %w", err)
	}

	return nil
}
