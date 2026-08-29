package lvm

import (
	"errors"
	"strings"

	runner "github.com/The127/go-runner"
)

// Client issues lvm2 commands against the local node. Client options
// set environment defaults, CommonOptions on a call override them.
type Client struct {
	runner     runner.Runner
	devices    []string
	autobackup *bool
}

// Option configures a Client.
type Option func(*Client)

// WithRunner substitutes the runner executing the lvm commands.
func WithRunner(r runner.Runner) Option {
	return func(c *Client) {
		c.runner = r
	}
}

// WithDevices restricts every command to the given devices, bypassing
// the system devices file. Needed to address devices the host lvm does
// not track, such as test loop devices.
func WithDevices(devices ...string) Option {
	return func(c *Client) {
		c.devices = devices
	}
}

// WithAutobackup controls whether lvm writes a metadata backup under
// /etc/lvm/backup after operations that change metadata.
func WithAutobackup(enabled bool) Option {
	return func(c *Client) {
		c.autobackup = &enabled
	}
}

// New returns a Client running lvm commands on this host.
func New(opts ...Option) *Client {
	c := &Client{}
	for _, opt := range opts {
		opt(c)
	}

	if c.runner == nil {
		c.runner = runner.New()
	}

	return c
}

var errAutobackupNotSupported = errors.New("this operation does not change metadata and does not support Autobackup")

func (c *Client) command(subcommand string, common CommonOptions) *runner.Cmd {
	devices := common.Devices
	if devices == nil {
		devices = c.devices
	}

	cmd := runner.Command("lvm", subcommand)
	if len(devices) > 0 {
		cmd = cmd.Append("--devices", strings.Join(devices, ","))
	}

	return cmd
}

// metadataCommand is command plus the autobackup flag, only valid for
// subcommands that change metadata.
func (c *Client) metadataCommand(subcommand string, common CommonOptions) *runner.Cmd {
	cmd := c.command(subcommand, common)

	autobackup := common.Autobackup
	if autobackup == nil {
		autobackup = c.autobackup
	}

	if autobackup != nil {
		cmd = cmd.Append("-A", flagValue(*autobackup))
	}

	return cmd
}
