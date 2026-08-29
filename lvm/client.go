package lvm

import (
	"strings"

	"github.com/The127/go-runner"
)

// Client issues lvm2 commands against the local node.
type Client struct {
	runner  runner.Runner
	devices []string
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

func (c *Client) command(subcommand string) *runner.Cmd {
	cmd := runner.Command("lvm", subcommand)
	if len(c.devices) > 0 {
		cmd = cmd.Append("--devices", strings.Join(c.devices, ","))
	}

	return cmd
}
