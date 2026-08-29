package lvm

import (
	"context"
	"errors"
	"testing"

	runner "github.com/The127/go-runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func failingClient(exitCode int, stderr string) *Client {
	return New(WithRunner(&fakeRunner{err: &runner.RunError{
		Name:     "lvm",
		ExitCode: exitCode,
		Stderr:   stderr,
	}}))
}

func TestHarvestedStderrClassification(t *testing.T) {
	cases := []struct {
		name     string
		exitCode int
		stderr   string
		want     error
	}{
		{"absent device", 5, "Cannot use /dev/beanstore-absent: device not found", ErrNotFound},
		{"untracked device", 5, "Cannot use /dev/loop0: device is not in devices file", ErrNotFound},
		{"absent vg", 5, `Volume group "absent-vg" not found
  Cannot process volume group absent-vg`, ErrNotFound},
		{"extend with existing member", 5, "Physical volume '/dev/loop0' is already in volume group 'beanstore-test-2769'", ErrAlreadyExists},
		{"duplicate vg name", 5, "A volume group called beanstore-vgtest-2772 already exists.", ErrAlreadyExists},
		{"pvcreate on vg member", 5, `Can't initialize physical volume "/dev/loop0" of volume group "errtest-vg" without -ff`, ErrInUse},
		{"pvremove on vg member", 5, "PV /dev/loop0 is used by VG errtest-vg so please use vgreduce first.", ErrInUse},
		{"non resizeable vg", 5, "Volume group beanstore-test-2770 is not resizeable.", ErrNotAllowed},
		{"unprivileged", 5, "/run/lock/lvm/P_global:aux: open failed: Permission denied", ErrPermission},
		{"command line parse error", 3, "Error during parsing of command line.", ErrInvalidCommand},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := failingClient(tc.exitCode, tc.stderr).
				RemovePhysicalVolume(t.Context(), "/dev/loop0", RemovePhysicalVolumeOptions{})

			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestUnclassifiedFailureStillCarriesDetails(t *testing.T) {
	err := failingClient(5, "something entirely new").
		RemovePhysicalVolume(t.Context(), "/dev/loop0", RemovePhysicalVolumeOptions{})

	var lvmErr *Error
	require.ErrorAs(t, err, &lvmErr)
	assert.Equal(t, 5, lvmErr.ExitCode)
	assert.Equal(t, "something entirely new", lvmErr.Stderr)
	assert.False(t, errors.Is(err, ErrNotFound))
	assert.False(t, errors.Is(err, ErrAlreadyExists))
	assert.False(t, errors.Is(err, ErrNotAllowed))
	assert.False(t, errors.Is(err, ErrInUse))
	assert.False(t, errors.Is(err, ErrPermission))
	assert.False(t, errors.Is(err, ErrInvalidCommand))
}

func TestRunnerErrorIsNotExposed(t *testing.T) {
	err := failingClient(5, "whatever").
		RemovePhysicalVolume(t.Context(), "/dev/loop0", RemovePhysicalVolumeOptions{})

	var runErr *runner.RunError
	assert.False(t, errors.As(err, &runErr))
}

func TestNonCommandFailuresPassThrough(t *testing.T) {
	client := New(WithRunner(&fakeRunner{err: context.Canceled}))

	err := client.RemovePhysicalVolume(t.Context(), "/dev/loop0", RemovePhysicalVolumeOptions{})

	assert.ErrorIs(t, err, context.Canceled)
}
