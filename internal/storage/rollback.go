package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/lvm"
)

// rollbackTargetTagPrefix persists which volume a rollback copy
// replaces. Distinct from targetTagPrefix, which carries a push
// address.
const rollbackTargetTagPrefix = "beanstore.rollback_target="

// ErrWrongLineage reports a rollback to a snapshot of a different
// volume.
var ErrWrongLineage = errors.New("snapshot does not belong to the volume")

// RollbackTempName derives the deterministic temp lv name. The name
// is caller mintable, so temps are identified by their tags only and
// a name collision cleanly fails the lvcreate.
func RollbackTempName(volumeID string) string {
	return volumeID + "+rb"
}

// BeginRollback checks the lineage and creates the transient rollback
// copy. The temp is born inactive behind the default skip flag.
func BeginRollback(ctx context.Context, client *lvm.Client, cfg config.Config, volumeID, snapshotID string) error {
	volume, err := GetVolume(ctx, client, cfg, volumeID)
	if err != nil {
		return err
	}
	if volume.State != StateReady {
		return &WrongStateError{Volume: volumeID, Found: volume.State}
	}

	snapshot, err := GetVolume(ctx, client, cfg, snapshotID)
	if err != nil {
		return err
	}
	if snapshot.State != StateSnapshot {
		return &WrongStateError{Volume: snapshotID, Found: snapshot.State}
	}
	if snapshot.Origin != volumeID {
		return fmt.Errorf("%w: %s is of %q", ErrWrongLineage, snapshotID, snapshot.Origin)
	}

	err = client.CreateThinSnapshot(ctx, cfg.VolumeGroup, snapshotID, RollbackTempName(volumeID),
		lvm.CreateThinSnapshotOptions{
			AddTags: []string{
				StateTag(StateRollback),
				rollbackTargetTagPrefix + volumeID,
			},
			Permission: lvm.PermissionReadWrite,
		})
	if err != nil {
		return fmt.Errorf("creating rollback copy for %s: %w", volumeID, err)
	}

	return nil
}

// FinishRollback renames the rollback copy onto the target id and
// retags it ready in one lvchange, which also clears the skip flag so
// Attach works. A copy already named the target only needs the
// retag.
func FinishRollback(ctx context.Context, client *lvm.Client, cfg config.Config, tempID, targetID string) error {
	if tempID != targetID {
		err := client.RenameLogicalVolume(ctx, cfg.VolumeGroup, tempID, targetID,
			lvm.RenameLogicalVolumeOptions{})
		if err != nil {
			return fmt.Errorf("renaming rollback copy of %s: %w", targetID, err)
		}
	}

	err := client.ChangeLogicalVolume(ctx, lvm.Name(cfg.VolumeGroup+"/"+targetID),
		lvm.ChangeLogicalVolumeOptions{
			AddTags: []string{StateTag(StateReady)},
			RemoveTags: []string{
				StateTag(StateRollback),
				rollbackTargetTagPrefix + targetID,
			},
			SetActivationSkip: lvm.Bool(false),
		})
	if err != nil {
		return fmt.Errorf("readying rolled back volume %s: %w", targetID, err)
	}

	return nil
}
