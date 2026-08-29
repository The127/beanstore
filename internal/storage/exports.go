package storage

import (
	"errors"
	"sync"
)

// ErrExportInProgress reports a delete refused because an export is
// streaming from the snapshot.
var ErrExportInProgress = errors.New("export in progress")

// ExportPins counts live exports per snapshot, so deletes can refuse
// while a stream reads the device. The pins are volatile like
// operation handles, a crash ends every stream.
type ExportPins struct {
	mu     sync.Mutex
	counts map[string]int
}

// NewExportPins returns an empty pin table.
func NewExportPins() *ExportPins {
	return &ExportPins{counts: map[string]int{}}
}

// Acquire pins the snapshot and reports whether this is its first
// live export.
func (p *ExportPins) Acquire(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.counts[id]++

	return p.counts[id] == 1
}

// Release unpins one export and reports whether it was the last.
func (p *ExportPins) Release(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.counts[id]--
	if p.counts[id] <= 0 {
		delete(p.counts, id)
		return true
	}

	return false
}

// Pinned reports whether any export streams from the snapshot.
func (p *ExportPins) Pinned(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.counts[id] > 0
}
