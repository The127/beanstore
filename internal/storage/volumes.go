package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/lvm"
)

// State is a volume's lifecycle state, persisted as an lvm tag.
type State string

// State values. StateUnknown marks a beanstore volume whose state tag
// value is unreadable.
const (
	StateCreating State = "creating"
	StateReady    State = "ready"
	StateAttached State = "attached"
	StatePushing  State = "pushing"
	StateIncoming State = "incoming"
	StateRetired  State = "retired"
	StateDeleting State = "deleting"
	StateUnknown  State = ""
)

// stateTagPrefix marks an lv as beanstore owned.
const stateTagPrefix = "beanstore.state="

// StateTag renders the lvm tag persisting the given state.
func StateTag(state State) string {
	return stateTagPrefix + string(state)
}

// Volume is one beanstore volume as read from the node's lvm state.
type Volume struct {
	ID        string
	State     State
	SizeBytes uint64
	UsedBytes uint64
}

// ListVolumes scans the configured pool for beanstore volumes. LVs
// without a state tag are foreign and skipped, a broken state tag is
// reported as StateUnknown.
func ListVolumes(ctx context.Context, client *lvm.Client, cfg config.Config) ([]Volume, error) {
	lvs, err := client.ListLogicalVolumes(ctx, lvm.ListLogicalVolumesOptions{
		VG:     cfg.VolumeGroup,
		Select: lvm.Select("pool_lv = " + cfg.ThinPool),
	})
	if err != nil {
		return nil, fmt.Errorf("scanning volumes: %w", err)
	}

	var volumes []Volume
	for _, lv := range lvs {
		state, owned := stateOf(lv.Tags)
		if !owned {
			continue
		}

		volumes = append(volumes, Volume{
			ID:        lv.Name,
			State:     state,
			SizeBytes: lv.SizeBytes,
			UsedBytes: uint64(float64(lv.SizeBytes) * lv.DataPercent / 100),
		})
	}

	return volumes, nil
}

func stateOf(tags []string) (State, bool) {
	for _, tag := range tags {
		value, found := strings.CutPrefix(tag, stateTagPrefix)
		if found {
			return knownState(value), true
		}
	}

	return StateUnknown, false
}

var knownStates = map[State]bool{
	StateCreating: true,
	StateReady:    true,
	StateAttached: true,
	StatePushing:  true,
	StateIncoming: true,
	StateRetired:  true,
	StateDeleting: true,
}

func knownState(value string) State {
	if knownStates[State(value)] {
		return State(value)
	}

	return StateUnknown
}
