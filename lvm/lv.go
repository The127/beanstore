package lvm

import (
	"context"
	"encoding/json"
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
