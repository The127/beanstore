package lvm

// Bool returns a pointer to the given value, for options fields where
// nil means unset.
func Bool(value bool) *bool {
	return &value
}

// CommonOptions are accepted by every operation and override the
// client's environment for one call.
type CommonOptions struct {
	// Devices overrides the client's device scoping. An empty non-nil
	// slice disables scoping for the call.
	Devices []Device
	// Autobackup overrides the client's autobackup setting. Only valid
	// on operations that change metadata.
	Autobackup *bool
}

// Selector selects which objects an operation addresses: Device,
// Select or All.
type Selector interface{ isSelector() }

// Device addresses one object by device path or name.
type Device string

func (Device) isSelector() {}

// Select addresses the objects matching lvm selection criteria, see
// lvmreport(7).
type Select string

func (Select) isSelector() {}

type allSelector struct{}

func (allSelector) isSelector() {}

// All addresses every object the command can see.
var All = allSelector{}
