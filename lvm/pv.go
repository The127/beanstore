package lvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PhysicalVolume is one pv as reported by pvs.
type PhysicalVolume struct {
	Device            Device
	UUID              string
	VolumeGroup       string
	SizeBytes         uint64
	FreeBytes         uint64
	DeviceSizeBytes   uint64
	Attributes        string
	Tags              []string
	Allocatable       bool
	Exported          bool
	Missing           bool
	InUse             bool
	Duplicate         bool
	MetadataAreas     uint64
	UsedMetadataAreas uint64
}

// CreatePhysicalVolumeOptions configures CreatePhysicalVolume.
type CreatePhysicalVolumeOptions struct {
	CommonOptions
	// Force creates without confirmation, wiping recognizable
	// signatures on the device.
	Force bool
}

// CreatePhysicalVolume initializes the given device for use by lvm.
// Without Force, devices carrying a recognizable signature are refused,
// lvm's prompt fails on the runner's closed stdin.
func (c *Client) CreatePhysicalVolume(ctx context.Context, device Device, opts CreatePhysicalVolumeOptions) error {
	if opts.Autobackup != nil {
		return errAutobackupNotSupported
	}

	cmd := c.command("pvcreate", opts.CommonOptions)
	if opts.Force {
		cmd = cmd.Append("-f")
	}
	cmd = cmd.Append(string(device))

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("creating physical volume %s: %w", device, err)
	}

	return nil
}

// ListPhysicalVolumesOptions configures ListPhysicalVolumes.
type ListPhysicalVolumesOptions struct {
	CommonOptions
	// Select filters the report by lvm selection criteria.
	Select Select
}

// ListPhysicalVolumes reports all pvs visible to the client.
func (c *Client) ListPhysicalVolumes(ctx context.Context, opts ListPhysicalVolumesOptions) ([]PhysicalVolume, error) {
	if opts.Autobackup != nil {
		return nil, errAutobackupNotSupported
	}

	cmd := c.command("pvs", opts.CommonOptions).Append(
		"--reportformat", "json",
		"--units", "b",
		"--nosuffix",
		"--binary",
		"-o", "pv_name,pv_uuid,vg_name,pv_size,pv_free,dev_size,pv_attr,pv_tags,"+
			"pv_allocatable,pv_exported,pv_missing,pv_in_use,pv_duplicate,"+
			"pv_mda_count,pv_mda_used_count",
	)
	if opts.Select != "" {
		cmd = cmd.Append("-S", string(opts.Select))
	}

	output, err := c.run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("listing physical volumes: %w", err)
	}

	return parsePVReport(output)
}

// RemovePhysicalVolumeOptions configures RemovePhysicalVolume.
type RemovePhysicalVolumeOptions struct {
	CommonOptions
}

// RemovePhysicalVolume wipes the lvm label from the given device. A pv
// belonging to a vg is refused.
func (c *Client) RemovePhysicalVolume(ctx context.Context, device Device, opts RemovePhysicalVolumeOptions) error {
	if opts.Autobackup != nil {
		return errAutobackupNotSupported
	}

	cmd := c.command("pvremove", opts.CommonOptions).Append(string(device))

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("removing physical volume %s: %w", device, err)
	}

	return nil
}

// ChangePhysicalVolumeOptions configures ChangePhysicalVolume. At least
// one property must be set.
type ChangePhysicalVolumeOptions struct {
	CommonOptions
	AddTags    []string
	RemoveTags []string
	// Allocatable controls whether lvm may allocate new extents on the
	// pv.
	Allocatable *bool
	// MetadataIgnore controls whether the metadata areas on the pv are
	// used to store vg metadata. lvm refuses to disable the last
	// metadata area of a vg.
	MetadataIgnore *bool
	// RegenerateUUID gives the pv a new random UUID, meant for pvs that
	// lost uniqueness through device cloning. On hosts tracking pvs by
	// UUID in the devices file, lvm updates the entry as part of the
	// command.
	RegenerateUUID bool
}

// ChangePhysicalVolume changes properties of the pvs the target
// selects. The pvs must be in a vg. All requested changes run as one
// lvm command.
func (c *Client) ChangePhysicalVolume(ctx context.Context, target Selector, opts ChangePhysicalVolumeOptions) error {
	cmd := c.metadataCommand("pvchange", opts.CommonOptions)

	properties := 0
	for _, tag := range opts.AddTags {
		cmd = cmd.Append("--addtag", tag)
		properties++
	}
	for _, tag := range opts.RemoveTags {
		cmd = cmd.Append("--deltag", tag)
		properties++
	}
	if opts.Allocatable != nil {
		cmd = cmd.Append("-x", flagValue(*opts.Allocatable))
		properties++
	}
	if opts.MetadataIgnore != nil {
		cmd = cmd.Append("--metadataignore", flagValue(*opts.MetadataIgnore))
		properties++
	}
	if opts.RegenerateUUID {
		cmd = cmd.Append("-u")
		properties++
	}

	if properties == 0 {
		return errors.New("changing a physical volume requires at least one property")
	}

	cmd, err := appendSelector(cmd, target)
	if err != nil {
		return err
	}

	_, err = c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("changing physical volumes %v: %w", target, err)
	}

	return nil
}

// ResizePhysicalVolumeOptions configures ResizePhysicalVolume.
type ResizePhysicalVolumeOptions struct {
	CommonOptions
	// SizeBytes overrides the automatically detected device size, meant
	// to shrink a pv before shrinking the underlying device. The
	// confirmation lvm asks for is answered, the explicit size already
	// expresses the intent. Zero detects the device size.
	SizeBytes uint64
}

// ResizePhysicalVolume resizes the given pv, by default to the current
// size of its underlying device.
func (c *Client) ResizePhysicalVolume(ctx context.Context, device Device, opts ResizePhysicalVolumeOptions) error {
	cmd := c.metadataCommand("pvresize", opts.CommonOptions)
	if opts.SizeBytes > 0 {
		cmd = cmd.Append(
			"--setphysicalvolumesize", strconv.FormatUint(opts.SizeBytes, 10)+"b",
			"-y",
		)
	}
	cmd = cmd.Append(string(device))

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("resizing physical volume %s: %w", device, err)
	}

	return nil
}

func flagValue(value bool) string {
	if value {
		return "y"
	}

	return "n"
}

type pvReport struct {
	Report []struct {
		PV []struct {
			Name        string `json:"pv_name"`
			UUID        string `json:"pv_uuid"`
			VG          string `json:"vg_name"`
			Size        string `json:"pv_size"`
			Free        string `json:"pv_free"`
			DevSize     string `json:"dev_size"`
			Attr        string `json:"pv_attr"`
			Tags        string `json:"pv_tags"`
			Allocatable string `json:"pv_allocatable"`
			Exported    string `json:"pv_exported"`
			Missing     string `json:"pv_missing"`
			InUse       string `json:"pv_in_use"`
			Duplicate   string `json:"pv_duplicate"`
			MDACount    string `json:"pv_mda_count"`
			MDAUsed     string `json:"pv_mda_used_count"`
		} `json:"pv"`
	} `json:"report"`
}

func parsePVReport(output []byte) ([]PhysicalVolume, error) {
	var report pvReport
	err := json.Unmarshal(output, &report)
	if err != nil {
		return nil, fmt.Errorf("parsing pvs report: %w", err)
	}

	var volumes []PhysicalVolume
	for _, r := range report.Report {
		for _, pv := range r.PV {
			size, err := strconv.ParseUint(pv.Size, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parsing size of %s: %w", pv.Name, err)
			}

			free, err := strconv.ParseUint(pv.Free, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parsing free space of %s: %w", pv.Name, err)
			}

			devSize, err := strconv.ParseUint(pv.DevSize, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parsing device size of %s: %w", pv.Name, err)
			}

			mdaCount, err := strconv.ParseUint(pv.MDACount, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parsing metadata area count of %s: %w", pv.Name, err)
			}

			mdaUsed, err := strconv.ParseUint(pv.MDAUsed, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parsing used metadata area count of %s: %w", pv.Name, err)
			}

			// tags may not contain commas, splitting is unambiguous
			var tags []string
			if pv.Tags != "" {
				tags = strings.Split(pv.Tags, ",")
			}

			volumes = append(volumes, PhysicalVolume{
				Device:            Device(pv.Name),
				UUID:              pv.UUID,
				VolumeGroup:       pv.VG,
				SizeBytes:         size,
				FreeBytes:         free,
				DeviceSizeBytes:   devSize,
				Attributes:        pv.Attr,
				Tags:              tags,
				Allocatable:       pv.Allocatable == "1",
				Exported:          pv.Exported == "1",
				Missing:           pv.Missing == "1",
				InUse:             pv.InUse == "1",
				Duplicate:         pv.Duplicate == "1",
				MetadataAreas:     mdaCount,
				UsedMetadataAreas: mdaUsed,
			})
		}
	}

	return volumes, nil
}

// DisplayPhysicalVolumeOptions configures DisplayPhysicalVolume.
type DisplayPhysicalVolumeOptions struct {
	CommonOptions
	// Maps includes the mapping of physical extents to lvs.
	Maps bool
}

// DisplayPhysicalVolume returns lvm's human readable description of the
// given pv. For machine readable data use ListPhysicalVolumes.
func (c *Client) DisplayPhysicalVolume(ctx context.Context, device Device, opts DisplayPhysicalVolumeOptions) (string, error) {
	if opts.Autobackup != nil {
		return "", errAutobackupNotSupported
	}

	cmd := c.command("pvdisplay", opts.CommonOptions)
	if opts.Maps {
		cmd = cmd.Append("-m")
	}
	cmd = cmd.Append(string(device))

	output, err := c.run(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("displaying physical volume %s: %w", device, err)
	}

	return string(output), nil
}

// ScanPhysicalVolumesOptions configures ScanPhysicalVolumes.
type ScanPhysicalVolumesOptions struct {
	CommonOptions
	// Device records only the pv on this device as online, all devices
	// when empty.
	Device Device
	// Autoactivate activates the lvs of any vg that became complete.
	Autoactivate bool
}

// ScanPhysicalVolumes updates lvm's runtime state of online pvs, the
// pvscan --cache form. Plain pv listing is ListPhysicalVolumes' job.
func (c *Client) ScanPhysicalVolumes(ctx context.Context, opts ScanPhysicalVolumesOptions) error {
	if opts.Autobackup != nil {
		return errAutobackupNotSupported
	}

	cmd := c.command("pvscan", opts.CommonOptions).Append("--cache")
	if opts.Autoactivate {
		cmd = cmd.Append("-aay")
	}
	if opts.Device != "" {
		cmd = cmd.Append(string(opts.Device))
	}

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("scanning physical volumes: %w", err)
	}

	return nil
}

// CheckPhysicalVolumeOptions configures CheckPhysicalVolume.
type CheckPhysicalVolumeOptions struct {
	CommonOptions
}

// CheckPhysicalVolume checks the lvm metadata on the given device.
// Findings go to lvm's log, the returned error is the verdict.
func (c *Client) CheckPhysicalVolume(ctx context.Context, device Device, opts CheckPhysicalVolumeOptions) error {
	if opts.Autobackup != nil {
		return errAutobackupNotSupported
	}

	cmd := c.command("pvck", opts.CommonOptions).Append(string(device))

	_, err := c.run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("checking physical volume %s: %w", device, err)
	}

	return nil
}

// DumpKind selects what DumpPhysicalVolume prints.
type DumpKind string

// DumpKind values.
const (
	DumpHeaders        DumpKind = "headers"
	DumpMetadata       DumpKind = "metadata"
	DumpMetadataAll    DumpKind = "metadata_all"
	DumpMetadataSearch DumpKind = "metadata_search"
)

// DumpPhysicalVolumeOptions configures DumpPhysicalVolume.
type DumpPhysicalVolumeOptions struct {
	CommonOptions
}

// DumpPhysicalVolume prints the on-disk lvm headers or metadata of the
// given device.
func (c *Client) DumpPhysicalVolume(ctx context.Context, device Device, kind DumpKind, opts DumpPhysicalVolumeOptions) (string, error) {
	if opts.Autobackup != nil {
		return "", errAutobackupNotSupported
	}

	cmd := c.command("pvck", opts.CommonOptions).Append("--dump", string(kind), string(device))

	output, err := c.run(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("dumping %s of physical volume %s: %w", kind, device, err)
	}

	return string(output), nil
}
