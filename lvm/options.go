package lvm

// Bool returns a pointer to the given value, for options fields where
// nil means unset.
func Bool(value bool) *bool {
	return &value
}

// CommonOptions are accepted by every operation and override the
// client's environment for one call.
type CommonOptions struct {
	// Devices overrides the client's device scoping.
	Devices []string
	// Autobackup overrides the client's autobackup setting. Only valid
	// on operations that change metadata.
	Autobackup *bool
}
