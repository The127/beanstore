package storage

import (
	"context"
	"errors"
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
	StateCreating   State = "creating"
	StateReady      State = "ready"
	StateAttached   State = "attached"
	StatePushing    State = "pushing"
	StateCommitting State = "committing"
	StateIncoming   State = "incoming"
	StateRetired    State = "retired"
	StateDeleting   State = "deleting"
	StateSnapshot   State = "snapshot"
	StateUnknown    State = ""
)

// stateTagPrefix marks an lv as beanstore owned.
const stateTagPrefix = "beanstore.state="

// originTagPrefix persists a snapshot's origin volume. The lvm origin
// field empties when the origin lv is removed, the tag does not.
const originTagPrefix = "beanstore.origin="

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
	// Path is the block device path, empty while inactive.
	Path string
	// Origin names the volume a snapshot was taken from, empty
	// otherwise. Tag backed, it survives the origin's deletion.
	Origin string
	// OriginTagged reports whether Origin came from the lineage tag
	// or from the lvm origin field of a pre-tag snapshot.
	OriginTagged bool
	Active       bool
	// Transfer is the transfer id of an INCOMING, PUSHING or
	// COMMITTING volume, empty otherwise.
	Transfer string
	// PushTarget is the destination address of a PUSHING or
	// COMMITTING volume, empty otherwise.
	PushTarget string
}

// ErrNotFound reports that no beanstore volume with the given id
// exists on this node.
var ErrNotFound = errors.New("volume does not exist")

// WrongStateError reports a verb hitting a volume in a state the verb
// does not accept.
type WrongStateError struct {
	Volume string
	Found  State
}

func (e *WrongStateError) Error() string {
	found := string(e.Found)
	if e.Found == StateUnknown {
		found = "unknown"
	}

	return fmt.Sprintf("volume %s is in state %s", e.Volume, found)
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
		_, owned := stateOf(lv.Tags)
		if !owned {
			continue
		}

		volumes = append(volumes, volumeFromLV(lv))
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

// GetVolume reads one beanstore volume. Foreign lvs do not count,
// looking one up returns ErrNotFound.
func GetVolume(ctx context.Context, client *lvm.Client, cfg config.Config, id string) (Volume, error) {
	lvs, err := client.ListLogicalVolumes(ctx, lvm.ListLogicalVolumesOptions{
		VG:     cfg.VolumeGroup,
		Select: lvm.Select("lv_name = " + id),
	})
	if err != nil {
		return Volume{}, fmt.Errorf("looking up volume %s: %w", id, err)
	}
	if len(lvs) == 0 {
		return Volume{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	_, owned := stateOf(lvs[0].Tags)
	if !owned {
		return Volume{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	return volumeFromLV(lvs[0]), nil
}

func volumeFromLV(lv lvm.LogicalVolume) Volume {
	state, _ := stateOf(lv.Tags)

	// non-snapshot states suppress the lvm origin field, a rolled
	// back or cloned volume is not a snapshot of anything
	origin := tagValue(lv.Tags, originTagPrefix)
	tagged := origin != ""
	if !tagged && state == StateSnapshot {
		origin = lv.Origin
	}

	return Volume{
		ID:           lv.Name,
		State:        state,
		SizeBytes:    lv.SizeBytes,
		UsedBytes:    uint64(float64(lv.SizeBytes) * lv.DataPercent / 100),
		Path:         lv.Path,
		Origin:       origin,
		OriginTagged: tagged,
		Active:       lv.Active,
		Transfer:     tagValue(lv.Tags, transferTagPrefix),
		PushTarget:   tagValue(lv.Tags, targetTagPrefix),
	}
}

func tagValue(tags []string, prefix string) string {
	for _, tag := range tags {
		value, found := strings.CutPrefix(tag, prefix)
		if found {
			return value
		}
	}

	return ""
}

// SnapshotsOf lists the ids of the volume's snapshots by their
// lineage tags.
func SnapshotsOf(ctx context.Context, client *lvm.Client, cfg config.Config, id string) ([]string, error) {
	volumes, err := ListVolumes(ctx, client, cfg)
	if err != nil {
		return nil, fmt.Errorf("listing snapshots of %s: %w", id, err)
	}

	var snapshots []string
	for _, volume := range volumes {
		if volume.State == StateSnapshot && volume.Origin == id {
			snapshots = append(snapshots, volume.ID)
		}
	}

	return snapshots, nil
}

// CreateSnapshot creates a thin snapshot of a READY or ATTACHED
// volume. The snapshot stays inactive, lvm flags it to be skipped on
// activation.
func CreateSnapshot(ctx context.Context, client *lvm.Client, cfg config.Config, originID, snapshotID string) error {
	origin, err := GetVolume(ctx, client, cfg, originID)
	if err != nil {
		return err
	}
	if origin.State != StateReady && origin.State != StateAttached {
		return &WrongStateError{Volume: originID, Found: origin.State}
	}

	err = client.CreateThinSnapshot(ctx, cfg.VolumeGroup, originID, snapshotID, lvm.CreateThinSnapshotOptions{
		AddTags:    []string{StateTag(StateSnapshot), originTagPrefix + originID},
		Permission: lvm.PermissionReadOnly,
	})
	if err != nil {
		return fmt.Errorf("creating snapshot %s of %s: %w", snapshotID, originID, err)
	}

	return nil
}

// DeleteSnapshot removes a snapshot. Only lvs in snapshot state
// qualify, volumes are removed through their delete flow. A snapshot
// with a live export refuses.
func DeleteSnapshot(ctx context.Context, client *lvm.Client, cfg config.Config, pins *ExportPins, id string) error {
	snapshot, err := GetVolume(ctx, client, cfg, id)
	if err != nil {
		return err
	}
	if snapshot.State != StateSnapshot {
		return &WrongStateError{Volume: id, Found: snapshot.State}
	}
	if pins.Pinned(id) {
		return fmt.Errorf("%w: %s", ErrExportInProgress, id)
	}

	return RemoveVolume(ctx, client, cfg, id)
}

// Attach activates a READY volume and returns its block device path.
// The tag flips before activation, so a crash leaves an attached
// volume that recovery re-activates.
func Attach(ctx context.Context, client *lvm.Client, cfg config.Config, id string) (string, error) {
	volume, err := GetVolume(ctx, client, cfg, id)
	if err != nil {
		return "", err
	}
	if volume.State != StateReady {
		return "", &WrongStateError{Volume: id, Found: volume.State}
	}

	name := lvm.Name(cfg.VolumeGroup + "/" + id)
	err = client.ChangeLogicalVolume(ctx, name, lvm.ChangeLogicalVolumeOptions{
		AddTags:    []string{StateTag(StateAttached)},
		RemoveTags: []string{StateTag(StateReady)},
	})
	if err != nil {
		return "", fmt.Errorf("attaching volume %s: %w", id, err)
	}

	err = client.ActivateLogicalVolume(ctx, name, lvm.ActivateLogicalVolumeOptions{})
	if err != nil {
		return "", fmt.Errorf("activating volume %s: %w", id, err)
	}

	volume, err = GetVolume(ctx, client, cfg, id)
	if err != nil {
		return "", err
	}

	return volume.Path, nil
}

// Detach deactivates an ATTACHED volume. Deactivation runs first, so
// an in-use device fails the call and the volume stays attached.
func Detach(ctx context.Context, client *lvm.Client, cfg config.Config, id string) error {
	volume, err := GetVolume(ctx, client, cfg, id)
	if err != nil {
		return err
	}
	if volume.State != StateAttached {
		return &WrongStateError{Volume: id, Found: volume.State}
	}

	name := lvm.Name(cfg.VolumeGroup + "/" + id)
	err = client.DeactivateLogicalVolume(ctx, name, lvm.DeactivateLogicalVolumeOptions{})
	if err != nil {
		return fmt.Errorf("deactivating volume %s: %w", id, err)
	}

	err = client.ChangeLogicalVolume(ctx, name, lvm.ChangeLogicalVolumeOptions{
		AddTags:    []string{StateTag(StateReady)},
		RemoveTags: []string{StateTag(StateAttached)},
	})
	if err != nil {
		return fmt.Errorf("detaching volume %s: %w", id, err)
	}

	return nil
}

// ErrShrink reports a resize below the volume's current size.
var ErrShrink = errors.New("volumes cannot shrink")

// ResizeVolume grows a READY or ATTACHED volume to the given size.
func ResizeVolume(ctx context.Context, client *lvm.Client, cfg config.Config, id string, sizeBytes uint64) error {
	volume, err := GetVolume(ctx, client, cfg, id)
	if err != nil {
		return err
	}
	if volume.State != StateReady && volume.State != StateAttached {
		return &WrongStateError{Volume: id, Found: volume.State}
	}
	if sizeBytes <= volume.SizeBytes {
		return fmt.Errorf("%w: volume %s has %d bytes, requested %d",
			ErrShrink, id, volume.SizeBytes, sizeBytes)
	}

	err = client.ResizeLogicalVolume(ctx, cfg.VolumeGroup+"/"+id, lvm.Bytes(sizeBytes),
		lvm.ResizeLogicalVolumeOptions{})
	if err != nil {
		return fmt.Errorf("resizing volume %s: %w", id, err)
	}

	return nil
}

// ErrHasSnapshots reports a volume delete without force while
// snapshots of the volume exist.
var ErrHasSnapshots = errors.New("volume has snapshots")

// MarkDeleting retags a READY or RETIRED volume as deleting and
// returns the snapshot ids to remove along with it. Without force,
// existing snapshots refuse the delete. The tags persist before the
// caller answers, a crash leaves deleting lvs that recovery removes.
func MarkDeleting(ctx context.Context, client *lvm.Client, cfg config.Config, pins *ExportPins, id string, force bool) ([]string, error) {
	volume, err := GetVolume(ctx, client, cfg, id)
	if err != nil {
		return nil, err
	}
	if volume.State != StateReady && volume.State != StateRetired {
		return nil, &WrongStateError{Volume: id, Found: volume.State}
	}

	snapshots, err := SnapshotsOf(ctx, client, cfg, id)
	if err != nil {
		return nil, err
	}
	if len(snapshots) > 0 && !force {
		return nil, fmt.Errorf("%w: %s", ErrHasSnapshots, id)
	}
	for _, snapshot := range snapshots {
		if pins.Pinned(snapshot) {
			return nil, fmt.Errorf("%w: %s", ErrExportInProgress, snapshot)
		}
	}

	for _, snapshot := range snapshots {
		err = client.ChangeLogicalVolume(ctx, lvm.Name(cfg.VolumeGroup+"/"+snapshot), lvm.ChangeLogicalVolumeOptions{
			AddTags:    []string{StateTag(StateDeleting)},
			RemoveTags: []string{StateTag(StateSnapshot)},
		})
		if err != nil {
			return nil, fmt.Errorf("marking snapshot %s deleting: %w", snapshot, err)
		}
	}

	err = client.ChangeLogicalVolume(ctx, lvm.Name(cfg.VolumeGroup+"/"+id), lvm.ChangeLogicalVolumeOptions{
		AddTags:    []string{StateTag(StateDeleting)},
		RemoveTags: []string{StateTag(volume.State)},
	})
	if err != nil {
		return nil, fmt.Errorf("marking volume %s deleting: %w", id, err)
	}

	return snapshots, nil
}

// RemoveVolume removes a volume's lv.
func RemoveVolume(ctx context.Context, client *lvm.Client, cfg config.Config, id string) error {
	err := client.RemoveLogicalVolume(ctx, lvm.Name(cfg.VolumeGroup+"/"+id), lvm.RemoveLogicalVolumeOptions{
		Force: true,
	})
	if err != nil {
		return fmt.Errorf("removing volume %s: %w", id, err)
	}

	return nil
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
	StateCreating:   true,
	StateReady:      true,
	StateAttached:   true,
	StatePushing:    true,
	StateCommitting: true,
	StateIncoming:   true,
	StateRetired:    true,
	StateDeleting:   true,
	StateSnapshot:   true,
}

func knownState(value string) State {
	if knownStates[State(value)] {
		return State(value)
	}

	return StateUnknown
}
