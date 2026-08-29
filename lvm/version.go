package lvm

import (
	"context"
	"fmt"
	"strings"
)

// Version is the lvm version report. Driver is empty when the kernel
// driver is not reachable.
type Version struct {
	LVM     string
	Library string
	Driver  string
}

// VersionOptions configures GetVersion.
type VersionOptions struct {
	CommonOptions
}

// GetVersion reports the installed lvm versions.
func (c *Client) GetVersion(ctx context.Context, opts VersionOptions) (Version, error) {
	if opts.Autobackup != nil {
		return Version{}, errAutobackupNotSupported
	}

	output, err := c.run(ctx, c.command("version", opts.CommonOptions))
	if err != nil {
		return Version{}, fmt.Errorf("reading lvm version: %w", err)
	}

	version := Version{}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		switch strings.TrimSpace(key) {
		case "LVM version":
			version.LVM = strings.TrimSpace(value)

		case "Library version":
			version.Library = strings.TrimSpace(value)

		case "Driver version":
			version.Driver = strings.TrimSpace(value)
		}
	}

	return version, nil
}
