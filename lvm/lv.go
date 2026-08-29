package lvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// LogicalVolume is one lv as reported by lvs.
type LogicalVolume struct {
	Name            string
	UUID            string
	VolumeGroup     string
	SizeBytes       uint64
	Attributes      string
	Tags            []string
	Pool            string
	Origin          string
	Path            string
	DevicePath      string
	DataPercent     float64
	MetadataPercent float64
	Active          bool
	Layout          []string
}

// CreateLogicalVolumeOptions configures CreateLogicalVolume.
type CreateLogicalVolumeOptions struct {
	CommonOptions
	AddTags []string
	// Activate controls whether the new lv is activated, lvm's default
	// when nil.
	Activate *bool
	// Permission sets the lv read/write permission.
	Permission Permission
	// Readahead sets the lv readahead.
	Readahead Readahead
	// Contiguous controls contiguous extent allocation.
	Contiguous *bool
	// Allocation sets the extent allocation policy.
	Allocation AllocationPolicy
	// SetActivationSkip controls the flag that makes activation skip
	// the lv unless IgnoreActivationSkip is used.
	SetActivationSkip *bool
	// IgnoreActivationSkip activates the lv even when flagged to be
	// skipped.
	IgnoreActivationSkip bool
	// SetAutoactivation controls whether the lv autoactivates on boot
	// and device appearance.
	SetAutoactivation *bool
	// Zero controls zeroing of the first 4KiB of the new lv.
	Zero *bool
	// WipeSignatures controls wiping of filesystem and raid signatures.
	WipeSignatures *bool
}

// CreateLogicalVolume creates a linear lv.
func (c *Client) CreateLogicalVolume(ctx context.Context, vg, name string, sizeBytes uint64, opts CreateLogicalVolumeOptions) error {
	cmd := c.metadataCommand("lvcreate", opts.CommonOptions).Append(
		"-L", strconv.FormatUint(sizeBytes, 10)+"b",
		"-n", name,
	)
	for _, tag := range opts.AddTags {
		cmd = cmd.Append("--addtag", tag)
	}
	if opts.Activate != nil {
		cmd = cmd.Append("-a", flagValue(*opts.Activate))
	}
	if opts.Permission != "" {
		cmd = cmd.Append("-p", string(opts.Permission))
	}
	if opts.Readahead != "" {
		cmd = cmd.Append("-r", string(opts.Readahead))
	}
	if opts.Contiguous != nil {
		cmd = cmd.Append("-C", flagValue(*opts.Contiguous))
	}
	if opts.Allocation != "" {
		cmd = cmd.Append("--alloc", string(opts.Allocation))
	}
	if opts.SetActivationSkip != nil {
		cmd = cmd.Append("-k", flagValue(*opts.SetActivationSkip))
	}
	if opts.IgnoreActivationSkip {
		cmd = cmd.Append("-K")
	}
	if opts.SetAutoactivation != nil {
		cmd = cmd.Append("--setautoactivation", flagValue(*opts.SetAutoactivation))
	}
	if opts.Zero != nil {
		cmd = cmd.Append("-Z", flagValue(*opts.Zero))
	}
	if opts.WipeSignatures != nil {
		cmd = cmd.Append("-W", flagValue(*opts.WipeSignatures))
	}
	cmd = cmd.Append(vg)

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("creating logical volume %s/%s: %w", vg, name, err)
	}

	return nil
}

// CreateThinPoolOptions configures CreateThinPool.
type CreateThinPoolOptions struct {
	CommonOptions
	AddTags []string
	// ChunkSizeBytes sets the pool chunk size, lvm's default when zero.
	ChunkSizeBytes uint64
	// MetadataSizeBytes sets the pool metadata lv size, lvm's default
	// when zero.
	MetadataSizeBytes uint64
	// Activate controls whether the new pool is activated, lvm's
	// default when nil.
	Activate *bool
	// Permission sets the pool read/write permission.
	Permission Permission
	// Readahead sets the pool readahead.
	Readahead Readahead
	// Contiguous controls contiguous extent allocation.
	Contiguous *bool
	// Allocation sets the extent allocation policy.
	Allocation AllocationPolicy
	// SetActivationSkip controls the flag that makes activation skip
	// the pool unless IgnoreActivationSkip is used.
	SetActivationSkip *bool
	// IgnoreActivationSkip activates the pool even when flagged to be
	// skipped.
	IgnoreActivationSkip bool
	// SetAutoactivation controls whether the pool autoactivates on
	// boot and device appearance.
	SetAutoactivation *bool
	// Zero controls zeroing of newly provisioned pool blocks.
	Zero *bool
	// Discards sets how the pool handles discards.
	Discards Discards
	// ErrorWhenFull makes writes to a full pool error instead of queue.
	ErrorWhenFull *bool
	// PoolMetadataSpare controls creation of the spare metadata lv in
	// the vg.
	PoolMetadataSpare *bool
}

// CreateThinPool creates a thin pool lv of the given data size.
func (c *Client) CreateThinPool(ctx context.Context, vg, name string, sizeBytes uint64, opts CreateThinPoolOptions) error {
	cmd := c.metadataCommand("lvcreate", opts.CommonOptions).Append(
		"--type", "thin-pool",
		"-L", strconv.FormatUint(sizeBytes, 10)+"b",
		"-n", name,
	)
	for _, tag := range opts.AddTags {
		cmd = cmd.Append("--addtag", tag)
	}
	if opts.ChunkSizeBytes > 0 {
		cmd = cmd.Append("-c", strconv.FormatUint(opts.ChunkSizeBytes, 10)+"b")
	}
	if opts.MetadataSizeBytes > 0 {
		cmd = cmd.Append("--poolmetadatasize", strconv.FormatUint(opts.MetadataSizeBytes, 10)+"b")
	}
	if opts.Activate != nil {
		cmd = cmd.Append("-a", flagValue(*opts.Activate))
	}
	if opts.Permission != "" {
		cmd = cmd.Append("-p", string(opts.Permission))
	}
	if opts.Readahead != "" {
		cmd = cmd.Append("-r", string(opts.Readahead))
	}
	if opts.Contiguous != nil {
		cmd = cmd.Append("-C", flagValue(*opts.Contiguous))
	}
	if opts.Allocation != "" {
		cmd = cmd.Append("--alloc", string(opts.Allocation))
	}
	if opts.SetActivationSkip != nil {
		cmd = cmd.Append("-k", flagValue(*opts.SetActivationSkip))
	}
	if opts.IgnoreActivationSkip {
		cmd = cmd.Append("-K")
	}
	if opts.SetAutoactivation != nil {
		cmd = cmd.Append("--setautoactivation", flagValue(*opts.SetAutoactivation))
	}
	if opts.Zero != nil {
		cmd = cmd.Append("-Z", flagValue(*opts.Zero))
	}
	if opts.Discards != "" {
		cmd = cmd.Append("--discards", string(opts.Discards))
	}
	if opts.ErrorWhenFull != nil {
		cmd = cmd.Append("--errorwhenfull", flagValue(*opts.ErrorWhenFull))
	}
	if opts.PoolMetadataSpare != nil {
		cmd = cmd.Append("--poolmetadataspare", flagValue(*opts.PoolMetadataSpare))
	}
	cmd = cmd.Append(vg)

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("creating thin pool %s/%s: %w", vg, name, err)
	}

	return nil
}

// CreateThinVolumeOptions configures CreateThinVolume.
type CreateThinVolumeOptions struct {
	CommonOptions
	AddTags []string
	// Activate controls whether the new lv is activated, lvm's default
	// when nil.
	Activate *bool
	// Permission sets the lv read/write permission.
	Permission Permission
	// Readahead sets the lv readahead.
	Readahead Readahead
	// Contiguous controls contiguous extent allocation.
	Contiguous *bool
	// Allocation sets the extent allocation policy.
	Allocation AllocationPolicy
	// SetActivationSkip controls the flag that makes activation skip
	// the lv unless IgnoreActivationSkip is used.
	SetActivationSkip *bool
	// IgnoreActivationSkip activates the lv even when flagged to be
	// skipped.
	IgnoreActivationSkip bool
	// SetAutoactivation controls whether the lv autoactivates on boot
	// and device appearance.
	SetAutoactivation *bool
	// Zero controls zeroing of the first 4KiB of the new lv.
	Zero *bool
	// WipeSignatures controls wiping of filesystem and raid signatures.
	WipeSignatures *bool
}

// CreateThinVolume creates a thin lv of the given virtual size in a
// thin pool.
func (c *Client) CreateThinVolume(ctx context.Context, vg, pool, name string, virtualSizeBytes uint64, opts CreateThinVolumeOptions) error {
	cmd := c.metadataCommand("lvcreate", opts.CommonOptions).Append(
		"--type", "thin",
		"--thinpool", pool,
		"-V", strconv.FormatUint(virtualSizeBytes, 10)+"b",
		"-n", name,
	)
	for _, tag := range opts.AddTags {
		cmd = cmd.Append("--addtag", tag)
	}
	if opts.Activate != nil {
		cmd = cmd.Append("-a", flagValue(*opts.Activate))
	}
	if opts.Permission != "" {
		cmd = cmd.Append("-p", string(opts.Permission))
	}
	if opts.Readahead != "" {
		cmd = cmd.Append("-r", string(opts.Readahead))
	}
	if opts.Contiguous != nil {
		cmd = cmd.Append("-C", flagValue(*opts.Contiguous))
	}
	if opts.Allocation != "" {
		cmd = cmd.Append("--alloc", string(opts.Allocation))
	}
	if opts.SetActivationSkip != nil {
		cmd = cmd.Append("-k", flagValue(*opts.SetActivationSkip))
	}
	if opts.IgnoreActivationSkip {
		cmd = cmd.Append("-K")
	}
	if opts.SetAutoactivation != nil {
		cmd = cmd.Append("--setautoactivation", flagValue(*opts.SetAutoactivation))
	}
	if opts.Zero != nil {
		cmd = cmd.Append("-Z", flagValue(*opts.Zero))
	}
	if opts.WipeSignatures != nil {
		cmd = cmd.Append("-W", flagValue(*opts.WipeSignatures))
	}
	cmd = cmd.Append(vg)

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("creating thin volume %s/%s: %w", vg, name, err)
	}

	return nil
}

// ListLogicalVolumesOptions configures ListLogicalVolumes.
type ListLogicalVolumesOptions struct {
	CommonOptions
	// VG narrows the report to one vg.
	VG string
	// Select filters the report by lvm selection criteria.
	Select Select
}

// ListLogicalVolumes reports all lvs visible to the client.
func (c *Client) ListLogicalVolumes(ctx context.Context, opts ListLogicalVolumesOptions) ([]LogicalVolume, error) {
	if opts.Autobackup != nil {
		return nil, errAutobackupNotSupported
	}

	cmd := c.command("lvs", opts.CommonOptions).Append(
		"--reportformat", "json",
		"--units", "b",
		"--nosuffix",
		"--binary",
		"-o", "lv_name,lv_uuid,vg_name,lv_size,lv_attr,lv_tags,pool_lv,origin,"+
			"lv_path,lv_dm_path,data_percent,metadata_percent,lv_active,lv_layout",
	)
	if opts.Select != "" {
		cmd = cmd.Append("-S", string(opts.Select))
	}
	if opts.VG != "" {
		cmd = cmd.Append(opts.VG)
	}

	output, err := c.run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("listing logical volumes: %w", err)
	}

	return parseLVReport(output)
}

// RemoveLogicalVolumeOptions configures RemoveLogicalVolume.
type RemoveLogicalVolumeOptions struct {
	CommonOptions
	// Force removes active and in use lvs without confirmation.
	Force bool
}

// RemoveLogicalVolume removes the lvs the target selects: a Name of the
// form vg/lv, a plain vg Name removing all its lvs, or Select criteria.
func (c *Client) RemoveLogicalVolume(ctx context.Context, target Selector, opts RemoveLogicalVolumeOptions) error {
	if opts.Autobackup != nil {
		return errAutobackupNotSupported
	}

	cmd := c.command("lvremove", opts.CommonOptions)
	if opts.Force {
		cmd = cmd.Append("-f")
	}

	cmd, err := appendSelector(cmd, target, "")
	if err != nil {
		return err
	}

	_, err = c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("removing logical volumes %v: %w", target, err)
	}

	return nil
}

type rawLV struct {
	Name            string `json:"lv_name"`
	UUID            string `json:"lv_uuid"`
	VG              string `json:"vg_name"`
	Size            string `json:"lv_size"`
	Attr            string `json:"lv_attr"`
	Tags            string `json:"lv_tags"`
	Pool            string `json:"pool_lv"`
	Origin          string `json:"origin"`
	Path            string `json:"lv_path"`
	DMPath          string `json:"lv_dm_path"`
	DataPercent     string `json:"data_percent"`
	MetadataPercent string `json:"metadata_percent"`
	Active          string `json:"lv_active"`
	Layout          string `json:"lv_layout"`
}

type lvReport struct {
	Report []struct {
		LV []rawLV `json:"lv"`
	} `json:"report"`
}

func parseLVReport(output []byte) ([]LogicalVolume, error) {
	var report lvReport
	err := json.Unmarshal(output, &report)
	if err != nil {
		return nil, fmt.Errorf("parsing lvs report: %w", err)
	}

	var volumes []LogicalVolume
	for _, lv := range flattenLVReport(report) {
		size, err := strconv.ParseUint(lv.Size, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing size of %s/%s: %w", lv.VG, lv.Name, err)
		}

		dataPercent, err := parsePercent(lv.DataPercent)
		if err != nil {
			return nil, fmt.Errorf("parsing data percent of %s/%s: %w", lv.VG, lv.Name, err)
		}

		metadataPercent, err := parsePercent(lv.MetadataPercent)
		if err != nil {
			return nil, fmt.Errorf("parsing metadata percent of %s/%s: %w", lv.VG, lv.Name, err)
		}

		volumes = append(volumes, LogicalVolume{
			Name:            lv.Name,
			UUID:            lv.UUID,
			VolumeGroup:     lv.VG,
			SizeBytes:       size,
			Attributes:      lv.Attr,
			Tags:            splitList(lv.Tags),
			Pool:            lv.Pool,
			Origin:          lv.Origin,
			Path:            lv.Path,
			DevicePath:      lv.DMPath,
			DataPercent:     dataPercent,
			MetadataPercent: metadataPercent,
			// lv_active reports "active" as a string on some lvm
			// versions and 0/1 with --binary on others
			Active: lv.Active == "1" || lv.Active == "active",
			Layout: splitList(lv.Layout),
		})
	}

	return volumes, nil
}

func flattenLVReport(report lvReport) []rawLV {
	var all []rawLV
	for _, r := range report.Report {
		all = append(all, r.LV...)
	}

	return all
}

// splitList splits lvm's comma joined list fields, the values may not
// contain commas.
func splitList(value string) []string {
	if value == "" {
		return nil
	}

	return strings.Split(value, ",")
}

// parsePercent parses lvm percent fields, empty for lvs without the
// concept.
func parsePercent(value string) (float64, error) {
	if value == "" {
		return 0, nil
	}

	return strconv.ParseFloat(value, 64)
}

// Permission is an lv read/write permission.
type Permission string

// Permission values.
const (
	PermissionReadWrite Permission = "rw"
	PermissionReadOnly  Permission = "r"
)

// Discards is how a thin pool handles discards.
type Discards string

// Discards values.
const (
	DiscardsIgnore     Discards = "ignore"
	DiscardsNoPassdown Discards = "nopassdown"
	DiscardsPassdown   Discards = "passdown"
)

// Readahead is an lv readahead setting: ReadaheadAuto, ReadaheadNone,
// or a fixed size from ReadaheadSectors.
type Readahead string

// Readahead values.
const (
	ReadaheadAuto Readahead = "auto"
	ReadaheadNone Readahead = "none"
)

// ReadaheadSectors is a fixed readahead of the given number of 512
// byte sectors.
func ReadaheadSectors(sectors uint64) Readahead {
	return Readahead(strconv.FormatUint(sectors, 10))
}

// ChangeLogicalVolumeOptions configures ChangeLogicalVolume. At least
// one property must be set. Zero, Discards and ErrorWhenFull are thin
// pool properties.
type ChangeLogicalVolumeOptions struct {
	CommonOptions
	AddTags    []string
	RemoveTags []string
	// Permission sets the lv read/write permission.
	Permission Permission
	// Contiguous controls contiguous extent allocation.
	Contiguous *bool
	// Zero controls zeroing of newly provisioned pool blocks.
	Zero *bool
	// Discards sets how the pool handles discards.
	Discards Discards
	// ErrorWhenFull makes writes to a full pool error instead of queue.
	ErrorWhenFull *bool
	// SetActivationSkip controls the flag that makes activation skip
	// the lv unless IgnoreActivationSkip is used.
	SetActivationSkip *bool
	// Readahead sets the lv readahead.
	Readahead Readahead
	// Allocation sets the extent allocation policy.
	Allocation AllocationPolicy
	// SetAutoactivation controls whether the lv autoactivates on boot
	// and device appearance.
	SetAutoactivation *bool
}

// ChangeLogicalVolume changes properties of the lvs the target selects.
// All requested changes run as one lvm command.
func (c *Client) ChangeLogicalVolume(ctx context.Context, target Selector, opts ChangeLogicalVolumeOptions) error {
	cmd := c.metadataCommand("lvchange", opts.CommonOptions)

	properties := 0
	for _, tag := range opts.AddTags {
		cmd = cmd.Append("--addtag", tag)
		properties++
	}
	for _, tag := range opts.RemoveTags {
		cmd = cmd.Append("--deltag", tag)
		properties++
	}
	if opts.Permission != "" {
		cmd = cmd.Append("-p", string(opts.Permission))
		properties++
	}
	if opts.Contiguous != nil {
		cmd = cmd.Append("-C", flagValue(*opts.Contiguous))
		properties++
	}
	if opts.Zero != nil {
		cmd = cmd.Append("-Z", flagValue(*opts.Zero))
		properties++
	}
	if opts.Discards != "" {
		cmd = cmd.Append("--discards", string(opts.Discards))
		properties++
	}
	if opts.ErrorWhenFull != nil {
		cmd = cmd.Append("--errorwhenfull", flagValue(*opts.ErrorWhenFull))
		properties++
	}
	if opts.SetActivationSkip != nil {
		cmd = cmd.Append("-k", flagValue(*opts.SetActivationSkip))
		properties++
	}
	if opts.Readahead != "" {
		cmd = cmd.Append("-r", string(opts.Readahead))
		properties++
	}
	if opts.Allocation != "" {
		cmd = cmd.Append("--alloc", string(opts.Allocation))
		properties++
	}
	if opts.SetAutoactivation != nil {
		cmd = cmd.Append("--setautoactivation", flagValue(*opts.SetAutoactivation))
		properties++
	}

	if properties == 0 {
		return errors.New("changing a logical volume requires at least one property")
	}

	cmd, err := appendSelector(cmd, target, "")
	if err != nil {
		return err
	}

	_, err = c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("changing logical volumes %v: %w", target, err)
	}

	return nil
}

// ActivateLogicalVolumeOptions configures ActivateLogicalVolume.
type ActivateLogicalVolumeOptions struct {
	CommonOptions
	// IgnoreActivationSkip also activates lvs flagged to be skipped.
	IgnoreActivationSkip bool
}

// ActivateLogicalVolume activates the lvs the target selects, giving
// them device nodes.
func (c *Client) ActivateLogicalVolume(ctx context.Context, target Selector, opts ActivateLogicalVolumeOptions) error {
	cmd := c.metadataCommand("lvchange", opts.CommonOptions).Append("-a", "y")
	if opts.IgnoreActivationSkip {
		cmd = cmd.Append("-K")
	}

	cmd, err := appendSelector(cmd, target, "")
	if err != nil {
		return err
	}

	_, err = c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("activating logical volumes %v: %w", target, err)
	}

	return nil
}

// DeactivateLogicalVolumeOptions configures DeactivateLogicalVolume.
type DeactivateLogicalVolumeOptions struct {
	CommonOptions
}

// DeactivateLogicalVolume deactivates the lvs the target selects,
// removing their device nodes.
func (c *Client) DeactivateLogicalVolume(ctx context.Context, target Selector, opts DeactivateLogicalVolumeOptions) error {
	cmd := c.metadataCommand("lvchange", opts.CommonOptions).Append("-a", "n")

	cmd, err := appendSelector(cmd, target, "")
	if err != nil {
		return err
	}

	_, err = c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("deactivating logical volumes %v: %w", target, err)
	}

	return nil
}

// ExtendLogicalVolumeOptions configures ExtendLogicalVolume.
type ExtendLogicalVolumeOptions struct {
	CommonOptions
	// Relative grows by the given size instead of to it.
	Relative bool
	// ResizeFilesystem also grows the filesystem on the lv.
	ResizeFilesystem bool
	// PoolMetadataSizeBytes also grows a thin pool's metadata lv to the
	// given size.
	PoolMetadataSizeBytes uint64
}

// ExtendLogicalVolume grows the given vg/lv to or by the given size.
func (c *Client) ExtendLogicalVolume(ctx context.Context, name string, sizeBytes uint64, opts ExtendLogicalVolumeOptions) error {
	size := strconv.FormatUint(sizeBytes, 10) + "b"
	if opts.Relative {
		size = "+" + size
	}

	cmd := c.metadataCommand("lvextend", opts.CommonOptions).Append("-L", size)
	if opts.ResizeFilesystem {
		cmd = cmd.Append("-r")
	}
	if opts.PoolMetadataSizeBytes > 0 {
		cmd = cmd.Append("--poolmetadatasize", strconv.FormatUint(opts.PoolMetadataSizeBytes, 10)+"b")
	}
	cmd = cmd.Append(name)

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("extending logical volume %s: %w", name, err)
	}

	return nil
}

// ReduceLogicalVolumeOptions configures ReduceLogicalVolume.
type ReduceLogicalVolumeOptions struct {
	CommonOptions
	// Relative shrinks by the given size instead of to it.
	Relative bool
	// ResizeFilesystem also shrinks the filesystem on the lv first.
	ResizeFilesystem bool
}

// ReduceLogicalVolume shrinks the given vg/lv to or by the given size,
// destroying data beyond the new end. The confirmation lvm asks for is
// answered, calling this already expresses the intent.
func (c *Client) ReduceLogicalVolume(ctx context.Context, name string, sizeBytes uint64, opts ReduceLogicalVolumeOptions) error {
	size := strconv.FormatUint(sizeBytes, 10) + "b"
	if opts.Relative {
		size = "-" + size
	}

	cmd := c.metadataCommand("lvreduce", opts.CommonOptions).Append("-L", size, "-f")
	if opts.ResizeFilesystem {
		cmd = cmd.Append("-r")
	}
	cmd = cmd.Append(name)

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("reducing logical volume %s: %w", name, err)
	}

	return nil
}

// ResizeLogicalVolumeOptions configures ResizeLogicalVolume.
type ResizeLogicalVolumeOptions struct {
	CommonOptions
	// ResizeFilesystem also resizes the filesystem on the lv.
	ResizeFilesystem bool
}

// ResizeLogicalVolume resizes the given vg/lv to the given absolute
// size. Shrinking prompts and fails, use ReduceLogicalVolume to shrink.
func (c *Client) ResizeLogicalVolume(ctx context.Context, name string, sizeBytes uint64, opts ResizeLogicalVolumeOptions) error {
	cmd := c.metadataCommand("lvresize", opts.CommonOptions).Append(
		"-L", strconv.FormatUint(sizeBytes, 10)+"b",
	)
	if opts.ResizeFilesystem {
		cmd = cmd.Append("-r")
	}
	cmd = cmd.Append(name)

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("resizing logical volume %s: %w", name, err)
	}

	return nil
}

// RenameLogicalVolumeOptions configures RenameLogicalVolume.
type RenameLogicalVolumeOptions struct {
	CommonOptions
}

// RenameLogicalVolume renames an lv within its vg.
func (c *Client) RenameLogicalVolume(ctx context.Context, vg, oldName, newName string, opts RenameLogicalVolumeOptions) error {
	cmd := c.metadataCommand("lvrename", opts.CommonOptions).Append(vg, oldName, newName)

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("renaming logical volume %s/%s to %s: %w", vg, oldName, newName, err)
	}

	return nil
}

// DisplayLogicalVolumeOptions configures DisplayLogicalVolume.
type DisplayLogicalVolumeOptions struct {
	CommonOptions
	// Maps includes the mapping of logical to physical extents.
	Maps bool
}

// DisplayLogicalVolume returns lvm's human readable description of the
// given vg/lv or vg, of all lvs when the name is empty. For machine
// readable data use ListLogicalVolumes.
func (c *Client) DisplayLogicalVolume(ctx context.Context, name string, opts DisplayLogicalVolumeOptions) (string, error) {
	if opts.Autobackup != nil {
		return "", errAutobackupNotSupported
	}

	cmd := c.command("lvdisplay", opts.CommonOptions)
	if opts.Maps {
		cmd = cmd.Append("-m")
	}
	if name != "" {
		cmd = cmd.Append(name)
	}

	output, err := c.run(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("displaying logical volume %s: %w", name, err)
	}

	return string(output), nil
}

// ScanLogicalVolumesOptions configures ScanLogicalVolumes.
type ScanLogicalVolumesOptions struct {
	CommonOptions
	// All also scans internal lvs like thin pool data and metadata.
	All bool
}

// ScanLogicalVolumes rescans all devices for lvs. Plain lv listing is
// ListLogicalVolumes' job.
func (c *Client) ScanLogicalVolumes(ctx context.Context, opts ScanLogicalVolumesOptions) error {
	if opts.Autobackup != nil {
		return errAutobackupNotSupported
	}

	cmd := c.command("lvscan", opts.CommonOptions)
	if opts.All {
		cmd = cmd.Append("-a")
	}

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("scanning logical volumes: %w", err)
	}

	return nil
}
