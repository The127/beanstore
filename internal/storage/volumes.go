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

// CreateVolume creates an inactive thin volume tagged creating and
// retags it ready once creation is complete.
func CreateVolume(ctx context.Context, client *lvm.Client, cfg config.Config, id string, sizeBytes uint64) error {
	err := client.CreateThinVolume(ctx, cfg.VolumeGroup, cfg.ThinPool, id, sizeBytes, lvm.CreateThinVolumeOptions{
		AddTags:  []string{StateTag(StateCreating)},
		Activate: lvm.Bool(false),
	})
	if err != nil {
		return fmt.Errorf("creating volume %s: %w", id, err)
	}

	err = client.ChangeLogicalVolume(ctx, lvm.Name(cfg.VolumeGroup+"/"+id), lvm.ChangeLogicalVolumeOptions{
		AddTags:    []string{StateTag(StateReady)},
		RemoveTags: []string{StateTag(StateCreating)},
	})
	if err != nil {
		return fmt.Errorf("readying volume %s: %w", id, err)
	}

	return nil
}

// VolumeExists reports whether any lv with the given name exists in
// the configured vg.
func VolumeExists(ctx context.Context, client *lvm.Client, cfg config.Config, id string) (bool, error) {
	lvs, err := client.ListLogicalVolumes(ctx, lvm.ListLogicalVolumesOptions{
		VG:     cfg.VolumeGroup,
		Select: lvm.Select("lv_name = " + id),
	})
	if err != nil {
		return false, fmt.Errorf("looking up volume %s: %w", id, err)
	}

	return len(lvs) > 0, nil
}

// NodeStatus is the node's capacity as read from the pool and its
// volumes.
type NodeStatus struct {
	PoolSizeBytes         uint64
	PoolUsedBytes         uint64
	PoolMetadataSizeBytes uint64
	PoolMetadataUsedBytes uint64
	CommittedBytes        uint64
	VolumeCounts          map[State]uint32
}

// GetNodeStatus reads the pool's usage and aggregates the volume scan.
func GetNodeStatus(ctx context.Context, client *lvm.Client, cfg config.Config) (NodeStatus, error) {
	pools, err := client.ListLogicalVolumes(ctx, lvm.ListLogicalVolumesOptions{
		VG:     cfg.VolumeGroup,
		Select: lvm.Select("lv_name = " + cfg.ThinPool),
	})
	if err != nil {
		return NodeStatus{}, fmt.Errorf("reading pool %s/%s: %w", cfg.VolumeGroup, cfg.ThinPool, err)
	}
	if len(pools) == 0 {
		return NodeStatus{}, fmt.Errorf("pool %s/%s is gone", cfg.VolumeGroup, cfg.ThinPool)
	}
	pool := pools[0]

	volumes, err := ListVolumes(ctx, client, cfg)
	if err != nil {
		return NodeStatus{}, err
	}

	status := NodeStatus{
		PoolSizeBytes:         pool.SizeBytes,
		PoolUsedBytes:         uint64(float64(pool.SizeBytes) * pool.DataPercent / 100),
		PoolMetadataSizeBytes: pool.MetadataSizeBytes,
		PoolMetadataUsedBytes: uint64(float64(pool.MetadataSizeBytes) * pool.MetadataPercent / 100),
		VolumeCounts:          map[State]uint32{},
	}
	for _, volume := range volumes {
		status.CommittedBytes += volume.SizeBytes
		status.VolumeCounts[volume.State]++
	}

	return status, nil
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
