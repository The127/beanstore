package operations

import (
	"fmt"
	"sync"
)

// Phase is an operation's state.
type Phase int

// Phase values.
const (
	Pending Phase = iota
	Progressing
	Done
	Failed
)

// Operation is the state of one operation.
type Operation struct {
	Phase     Phase
	BytesDone uint64
	Reason    string
}

// Table tracks operations by their caller minted ids.
type Table struct {
	mu  sync.Mutex
	ops map[string]Operation
}

// NewTable returns an empty operation table.
func NewTable() *Table {
	return &Table{ops: map[string]Operation{}}
}

// Begin registers a new pending operation. Ids are single use, reuse
// is an error.
func (t *Table) Begin(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.ops[id]; exists {
		return fmt.Errorf("operation %s already exists", id)
	}
	t.ops[id] = Operation{Phase: Pending}

	return nil
}

// Progress records how many bytes the operation has processed.
func (t *Table) Progress(id string, bytesDone uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.ops[id]; exists {
		t.ops[id] = Operation{Phase: Progressing, BytesDone: bytesDone}
	}
}

// Done marks the operation as finished successfully.
func (t *Table) Done(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.ops[id]; exists {
		t.ops[id] = Operation{Phase: Done}
	}
}

// Fail marks the operation as failed with the given reason.
func (t *Table) Fail(id, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.ops[id]; exists {
		t.ops[id] = Operation{Phase: Failed, Reason: reason}
	}
}

// Get returns the operation's current state.
func (t *Table) Get(id string) (Operation, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	op, exists := t.ops[id]

	return op, exists
}
