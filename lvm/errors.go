package lvm

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors matched by errors.Is on classified failures.
var (
	// ErrNotFound reports an addressed object that does not exist or is
	// not visible to lvm.
	ErrNotFound = errors.New("lvm object not found")
	// ErrAlreadyExists reports a name collision with an existing object.
	ErrAlreadyExists = errors.New("lvm object already exists")
	// ErrInUse reports an object refused because something else uses it.
	ErrInUse = errors.New("lvm object in use")
	// ErrNotAllowed reports an operation refused by the object's state
	// or configuration.
	ErrNotAllowed = errors.New("lvm operation not allowed")
	// ErrPermission reports missing privileges for lvm's devices, locks
	// or metadata.
	ErrPermission = errors.New("lvm permission denied")
	// ErrInvalidCommand reports a command line lvm could not parse,
	// which is a bug in this library or in passed criteria.
	ErrInvalidCommand = errors.New("invalid lvm command")
)

// Error is a failed lvm command. Classified failures additionally match
// one of the sentinel errors via errors.Is.
type Error struct {
	ExitCode int
	Stderr   string
	kind     error
}

func (e *Error) Error() string {
	return fmt.Sprintf("lvm exited with %d: %s", e.ExitCode, e.Stderr)
}

// Is matches the sentinel error this failure was classified as.
func (e *Error) Is(target error) bool {
	return e.kind != nil && target == e.kind
}

const invalidCommandExitCode = 3

// stderr patterns as emitted by lvm2 in the C locale, every entry
// backed by a harvested message and pinned by an integration test
var stderrPatterns = []struct {
	substring string
	kind      error
}{
	{"Permission denied", ErrPermission},
	{": device not found", ErrNotFound},
	{"is not in devices file", ErrNotFound},
	{`" not found`, ErrNotFound},
	{"already exists", ErrAlreadyExists},
	{"is already in volume group", ErrAlreadyExists},
	{"is used by VG", ErrInUse},
	{"is not resizeable", ErrNotAllowed},
	{"without -ff", ErrInUse},
}

func classify(exitCode int, stderr string) error {
	if exitCode == invalidCommandExitCode {
		return ErrInvalidCommand
	}

	for _, pattern := range stderrPatterns {
		if strings.Contains(stderr, pattern.substring) {
			return pattern.kind
		}
	}

	return nil
}
