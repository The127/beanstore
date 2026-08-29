package lvm

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PhysicalVolume is one pv as reported by pvs.
type PhysicalVolume struct {
	Device      string
	VolumeGroup string
	SizeBytes   uint64
	FreeBytes   uint64
	Attributes  string
	Tags        []string
}

// CreatePhysicalVolume initializes the given device for use by lvm. It
// refuses devices carrying a recognizable signature, lvm's prompt fails
// on the runner's closed stdin.
func (c *Client) CreatePhysicalVolume(ctx context.Context, device string) error {
	cmd := c.command("pvcreate").Append(device)

	_, err := c.runner.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("creating physical volume %s: %w", device, err)
	}

	return nil
}

// ListPhysicalVolumes reports all pvs visible to the client.
func (c *Client) ListPhysicalVolumes(ctx context.Context) ([]PhysicalVolume, error) {
	cmd := c.command("pvs").Append(
		"--reportformat", "json",
		"--units", "b",
		"--nosuffix",
		"-o", "pv_name,vg_name,pv_size,pv_free,pv_attr,pv_tags",
	)

	output, err := c.runner.Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("listing physical volumes: %w", err)
	}

	return parsePVReport(output)
}

// RemovePhysicalVolume wipes the lvm label from the given device.
func (c *Client) RemovePhysicalVolume(ctx context.Context, device string) error {
	cmd := c.command("pvremove").Append(device)

	_, err := c.runner.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("removing physical volume %s: %w", device, err)
	}

	return nil
}

type pvReport struct {
	Report []struct {
		PV []struct {
			Name string `json:"pv_name"`
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

// AddPhysicalVolumeTag tags the given pv, which must be in a vg.
func (c *Client) AddPhysicalVolumeTag(ctx context.Context, device, tag string) error {
	cmd := c.command("pvchange").Append("--addtag", tag, device)

	_, err := c.runner.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("tagging physical volume %s with %s: %w", device, tag, err)
	}

	return nil
}

// RemovePhysicalVolumeTag removes a tag from the given pv.
func (c *Client) RemovePhysicalVolumeTag(ctx context.Context, device, tag string) error {
	cmd := c.command("pvchange").Append("--deltag", tag, device)

	_, err := c.runner.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("untagging physical volume %s from %s: %w", device, tag, err)
	}

	return nil
}

// SetPhysicalVolumeAllocatable controls whether lvm may allocate new
// extents on the given pv.
func (c *Client) SetPhysicalVolumeAllocatable(ctx context.Context, device string, allocatable bool) error {
	value := "n"
	if allocatable {
		value = "y"
	}

	cmd := c.command("pvchange").Append("-x", value, device)

	_, err := c.runner.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("setting physical volume %s allocatable=%s: %w", device, value, err)
	}

	return nil
}

// ResizePhysicalVolume grows the given pv to the current size of its
// underlying device.
func (c *Client) ResizePhysicalVolume(ctx context.Context, device string) error {
	cmd := c.command("pvresize").Append(device)

	_, err := c.runner.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("resizing physical volume %s: %w", device, err)
	}

	return nil
}
