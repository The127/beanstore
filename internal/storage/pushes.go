package storage

import (
	"context"
	"fmt"

	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/lvm"
)

// targetTagPrefix persists a push's destination address on its volume.
const targetTagPrefix = "beanstore.target="

// MarkPushing retags a READY volume as pushing and persists the
// transfer id and destination address for use after a restart.
func MarkPushing(ctx context.Context, client *lvm.Client, cfg config.Config, id, transferID, target string) error {
	volume, err := GetVolume(ctx, client, cfg, id)
	if err != nil {
		return err
	}
	if volume.State != StateReady {
		return &WrongStateError{Volume: id, Found: volume.State}
	}

	err = client.ChangeLogicalVolume(ctx, lvm.Name(cfg.VolumeGroup+"/"+id), lvm.ChangeLogicalVolumeOptions{
		AddTags: []string{
			StateTag(StatePushing),
			transferTagPrefix + transferID,
			targetTagPrefix + target,
		},
		RemoveTags: []string{StateTag(StateReady)},
	})
	if err != nil {
		return fmt.Errorf("marking volume %s pushing: %w", id, err)
	}

	return nil
}

// MarkCommitting retags a PUSHING volume as committing. Must run
// before CommitTransfer is sent.
func MarkCommitting(ctx context.Context, client *lvm.Client, cfg config.Config, id string) error {
	volume, err := GetVolume(ctx, client, cfg, id)
	if err != nil {
		return err
	}
	if volume.State != StatePushing {
		return &WrongStateError{Volume: id, Found: volume.State}
	}

	err = client.ChangeLogicalVolume(ctx, lvm.Name(cfg.VolumeGroup+"/"+id), lvm.ChangeLogicalVolumeOptions{
		AddTags:    []string{StateTag(StateCommitting)},
		RemoveTags: []string{StateTag(StatePushing)},
	})
	if err != nil {
		return fmt.Errorf("marking volume %s committing: %w", id, err)
	}

	return nil
}

// RetirePushed retags a COMMITTING volume as retired and drops the
// push tags. Only valid once the destination has committed.
func RetirePushed(ctx context.Context, client *lvm.Client, cfg config.Config, id string) error {
	volume, err := GetVolume(ctx, client, cfg, id)
	if err != nil {
		return err
	}
	if volume.State != StateCommitting {
		return &WrongStateError{Volume: id, Found: volume.State}
	}

	err = deactivatePushed(ctx, client, cfg, volume)
	if err != nil {
		return err
	}

	err = client.ChangeLogicalVolume(ctx, lvm.Name(cfg.VolumeGroup+"/"+id), lvm.ChangeLogicalVolumeOptions{
		AddTags: []string{StateTag(StateRetired)},
		RemoveTags: []string{
			StateTag(StateCommitting),
			transferTagPrefix + volume.Transfer,
			targetTagPrefix + volume.PushTarget,
		},
	})
	if err != nil {
		return fmt.Errorf("retiring volume %s: %w", id, err)
	}

	return nil
}

// AbortPush retags a PUSHING or COMMITTING volume back to READY and
// drops the push tags. From COMMITTING only valid once the
// destination is known to not have committed.
func AbortPush(ctx context.Context, client *lvm.Client, cfg config.Config, id string) error {
	volume, err := GetVolume(ctx, client, cfg, id)
	if err != nil {
		return err
	}
	if volume.State != StatePushing && volume.State != StateCommitting {
		return &WrongStateError{Volume: id, Found: volume.State}
	}

	err = deactivatePushed(ctx, client, cfg, volume)
	if err != nil {
		return err
	}

	err = client.ChangeLogicalVolume(ctx, lvm.Name(cfg.VolumeGroup+"/"+id), lvm.ChangeLogicalVolumeOptions{
		AddTags: []string{StateTag(StateReady)},
		RemoveTags: []string{
			StateTag(volume.State),
			transferTagPrefix + volume.Transfer,
			targetTagPrefix + volume.PushTarget,
		},
	})
	if err != nil {
		return fmt.Errorf("aborting push of volume %s: %w", id, err)
	}

	return nil
}

// deactivatePushed removes the device a push read from, READY and
// RETIRED volumes are inactive.
func deactivatePushed(ctx context.Context, client *lvm.Client, cfg config.Config, volume Volume) error {
	if !volume.Active {
		return nil
	}

	err := client.DeactivateLogicalVolume(ctx, lvm.Name(cfg.VolumeGroup+"/"+volume.ID),
		lvm.DeactivateLogicalVolumeOptions{})
	if err != nil {
		return fmt.Errorf("deactivating pushed volume %s: %w", volume.ID, err)
	}

	return nil
}
