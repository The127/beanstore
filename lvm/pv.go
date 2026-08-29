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
	Device      string
	UUID        string
	VolumeGroup string
	SizeBytes   uint64
	FreeBytes   uint64
	Attributes  string
	Tags        []string
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
func (c *Client) CreatePhysicalVolume(ctx context.Context, device string, opts CreatePhysicalVolumeOptions) error {
	if opts.Autobackup != nil {
		return errAutobackupNotSupported
	}

	cmd := c.command("pvcreate", opts.CommonOptions)
	if opts.Force {
		cmd = cmd.Append("-f")
	}
	cmd = cmd.Append(device)

	_, err := c.runner.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("creating physical volume %s: %w", device, err)
	}

	return nil
}

// ListPhysicalVolumesOptions configures ListPhysicalVolumes.
type ListPhysicalVolumesOptions struct {
	CommonOptions
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
		"-o", "pv_name,pv_uuid,vg_name,pv_size,pv_free,pv_attr,pv_tags",
	)

	output, err := c.runner.Run(ctx, cmd)
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
func (c *Client) RemovePhysicalVolume(ctx context.Context, device string, opts RemovePhysicalVolumeOptions) error {
	if opts.Autobackup != nil {
		return errAutobackupNotSupported
	}

	cmd := c.command("pvremove", opts.CommonOptions).Append(device)

	_, err := c.runner.Run(ctx, cmd)
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

// ChangePhysicalVolume changes properties of the given pv, which must
// be in a vg. All requested changes run as one lvm command.
func (c *Client) ChangePhysicalVolume(ctx context.Context, device string, opts ChangePhysicalVolumeOptions) error {
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

	cmd = cmd.Append(device)

	_, err := c.runner.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("changing physical volume %s: %w", device, err)
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
func (c *Client) ResizePhysicalVolume(ctx context.Context, device string, opts ResizePhysicalVolumeOptions) error {
	cmd := c.metadataCommand("pvresize", opts.CommonOptions)
	if opts.SizeBytes > 0 {
		cmd = cmd.Append(
			"--setphysicalvolumesize", strconv.FormatUint(opts.SizeBytes, 10)+"b",
			"-y",
		)
	}
	cmd = cmd.Append(device)

	_, err := c.runner.Run(ctx, cmd)
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
			Name string `json:"pv_name"`
			UUID string `json:"pv_uuid"`
			VG   string `json:"vg_name"`
			Size string `json:"pv_size"`
			Free string `json:"pv_free"`
			Attr string `json:"pv_attr"`
			Tags string `json:"pv_tags"`
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

			// tags may not contain commas, splitting is unambiguous
			var tags []string
			if pv.Tags != "" {
				tags = strings.Split(pv.Tags, ",")
			}

			volumes = append(volumes, PhysicalVolume{
				Device:      pv.Name,
				UUID:        pv.UUID,
				VolumeGroup: pv.VG,
				SizeBytes:   size,
				FreeBytes:   free,
				Attributes:  pv.Attr,
				Tags:        tags,
			})
		}
	}

	return volumes, nil
}
