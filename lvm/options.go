package lvm

import "strconv"

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

// Name addresses one object by its lvm name, like a vg.
type Name string

func (Name) isSelector() {}

// Select addresses the objects matching lvm selection criteria, see
// lvmreport(7).
type Select string

func (Select) isSelector() {}

type allSelector struct{}

func (allSelector) isSelector() {}

// All addresses every object the command can see.
var All = allSelector{}

// Size is a size expression for resize operations: Bytes, Extents or
// Percent, made relative with GrowBy or ShrinkBy.
type Size interface {
	// sizeArg returns the lvm flag and value rendering the size.
	sizeArg() (flag, value string)
	// direction is 0 for absolute sizes, positive for GrowBy and
	// negative for ShrinkBy.
	direction() int
}

type sizeBytes uint64

func (s sizeBytes) sizeArg() (string, string) {
	return "-L", strconv.FormatUint(uint64(s), 10) + "b"
}

func (sizeBytes) direction() int { return 0 }

// Bytes is an absolute size in bytes.
func Bytes(n uint64) Size { return sizeBytes(n) }

type sizeExtents uint64

func (s sizeExtents) sizeArg() (string, string) {
	return "-l", strconv.FormatUint(uint64(s), 10)
}

func (sizeExtents) direction() int { return 0 }

// Extents is an absolute size in vg physical extents.
func Extents(n uint64) Size { return sizeExtents(n) }

// PercentBase is what a Percent size is relative to.
type PercentBase string

// PercentBase values.
const (
	PercentVG     PercentBase = "VG"
	PercentFree   PercentBase = "FREE"
	PercentPVS    PercentBase = "PVS"
	PercentOrigin PercentBase = "ORIGIN"
)

type sizePercent struct {
	percent uint64
	base    PercentBase
}

func (s sizePercent) sizeArg() (string, string) {
	return "-l", strconv.FormatUint(s.percent, 10) + "%" + string(s.base)
}

func (sizePercent) direction() int { return 0 }

// Percent is a size relative to the given base, see lvresize(8) for
// the per base semantics.
func Percent(percent uint64, base PercentBase) Size {
	return sizePercent{percent: percent, base: base}
}

type relativeSize struct {
	inner Size
	sign  string
}

func (s relativeSize) sizeArg() (string, string) {
	flag, value := s.inner.sizeArg()
	return flag, s.sign + value
}

func (s relativeSize) direction() int {
	if s.sign == "+" {
		return 1
	}

	return -1
}

func absolute(size Size) Size {
	if relative, ok := size.(relativeSize); ok {
		return relative.inner
	}

	return size
}

// GrowBy makes the given size an increase instead of a target.
func GrowBy(size Size) Size {
	return relativeSize{inner: absolute(size), sign: "+"}
}

// ShrinkBy makes the given size a decrease instead of a target.
func ShrinkBy(size Size) Size {
	return relativeSize{inner: absolute(size), sign: "-"}
}
