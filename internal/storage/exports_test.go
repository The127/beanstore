package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/The127/beanstore/lvm"
)

func TestExportPinsCount(t *testing.T) {
	pins := NewExportPins()

	assert.False(t, pins.Pinned("snap-1"))
	assert.True(t, pins.Acquire("snap-1"), "first export")
	assert.False(t, pins.Acquire("snap-1"), "second export")
	assert.True(t, pins.Pinned("snap-1"))

	assert.False(t, pins.Release("snap-1"), "one export still live")
	assert.True(t, pins.Release("snap-1"), "last export")
	assert.False(t, pins.Pinned("snap-1"))
}

func TestDeleteSnapshotRefusesLiveExport(t *testing.T) {
	fake := &fakeRunner{outputs: []string{snapshotLV}}
	client := lvm.New(lvm.WithRunner(fake))
	pins := NewExportPins()
	pins.Acquire("snap-1")

	err := DeleteSnapshot(t.Context(), client, testConfig(), pins, "snap-1")

	assert.ErrorIs(t, err, ErrExportInProgress)
	assert.Len(t, fake.calls, 1, "no lvremove ran")
}

func TestForceDeleteRefusesLiveExport(t *testing.T) {
	fake := &fakeRunner{outputs: []string{readyLV, snapshotLV}}
	client := lvm.New(lvm.WithRunner(fake))
	pins := NewExportPins()
	pins.Acquire("snap-1")

	_, err := MarkDeleting(t.Context(), client, testConfig(), pins, "vol-1", true)

	assert.ErrorIs(t, err, ErrExportInProgress)
	assert.Len(t, fake.calls, 2, "no tags changed")
}
