package operations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOperationLifecycle(t *testing.T) {
	table := NewTable()

	require.NoError(t, table.Begin("op-1"))

	op, exists := table.Get("op-1")
	require.True(t, exists)
	assert.Equal(t, Pending, op.Phase)

	table.Progress("op-1", 42)
	op, _ = table.Get("op-1")
	assert.Equal(t, Progressing, op.Phase)
	assert.Equal(t, uint64(42), op.BytesDone)

	table.Done("op-1")
	op, _ = table.Get("op-1")
	assert.Equal(t, Done, op.Phase)
}

func TestBeginRefusesReuse(t *testing.T) {
	table := NewTable()

	require.NoError(t, table.Begin("op-1"))
	table.Done("op-1")

	assert.ErrorContains(t, table.Begin("op-1"), "already exists")
}

func TestFailRecordsReason(t *testing.T) {
	table := NewTable()

	require.NoError(t, table.Begin("op-1"))
	table.Fail("op-1", "pool exploded")

	op, _ := table.Get("op-1")
	assert.Equal(t, Failed, op.Phase)
	assert.Equal(t, "pool exploded", op.Reason)
}

func TestGetUnknownOperation(t *testing.T) {
	_, exists := NewTable().Get("op-9")

	assert.False(t, exists)
}

func TestUpdatesToUnknownOperationsAreIgnored(t *testing.T) {
	table := NewTable()

	table.Done("op-9")
	table.Fail("op-9", "boom")
	table.Progress("op-9", 1)

	_, exists := table.Get("op-9")
	assert.False(t, exists)
}
