package storage

import (
	"context"

	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/internal/logging"
	"github.com/The127/beanstore/lvm"
)

// Recover applies the crash rules to every volume at startup: creating
// volumes are unfinished garbage and removed, deleting volumes finish
// their removal, attached volumes are re-activated since activation
// does not survive a reboot, pushing volumes revert to ready.
// Committing volumes stay put until the push resolver settles them.
// Per volume failures are logged and leave the volume in its state,
// the remaining volumes still recover.
func Recover(ctx context.Context, client *lvm.Client, cfg config.Config) error {
	volumes, err := ListVolumes(ctx, client, cfg)
	if err != nil {
		return err
	}

	log := logging.FromContext(ctx)
	for _, volume := range volumes {
		switch volume.State {
		case StateCreating, StateDeleting:
			err = RemoveVolume(ctx, client, cfg, volume.ID)
			if err != nil {
				log.Error("removing volume during recovery",
					"volume", volume.ID, "state", string(volume.State), "error", err)
				continue
			}
			log.Info("removed volume during recovery",
				"volume", volume.ID, "state", string(volume.State))

		case StateAttached:
			err = client.ActivateLogicalVolume(ctx,
				lvm.Name(cfg.VolumeGroup+"/"+volume.ID), lvm.ActivateLogicalVolumeOptions{})
			if err != nil {
				log.Error("re-activating volume during recovery",
					"volume", volume.ID, "error", err)
				continue
			}
			log.Info("re-activated attached volume", "volume", volume.ID)

		case StateSnapshot:
			// pre-tag snapshots get their lineage tag while the lvm
			// origin field still carries it
			if !volume.OriginTagged && volume.Origin != "" {
				err = client.ChangeLogicalVolume(ctx,
					lvm.Name(cfg.VolumeGroup+"/"+volume.ID), lvm.ChangeLogicalVolumeOptions{
						AddTags: []string{originTagPrefix + volume.Origin},
					})
				if err != nil {
					log.Error("backfilling snapshot lineage during recovery",
						"volume", volume.ID, "error", err)
					continue
				}
				log.Info("backfilled snapshot lineage", "volume", volume.ID, "origin", volume.Origin)
			}

			// a crash mid-export leaves the snapshot active
			if !volume.Active {
				continue
			}
			err = client.DeactivateLogicalVolume(ctx,
				lvm.Name(cfg.VolumeGroup+"/"+volume.ID), lvm.DeactivateLogicalVolumeOptions{})
			if err != nil {
				log.Error("deactivating snapshot during recovery",
					"volume", volume.ID, "error", err)
				continue
			}
			log.Info("deactivated stray active snapshot", "volume", volume.ID)

		case StateIncoming:
			// no transfer session survives a restart
			err = RemoveVolume(ctx, client, cfg, volume.ID)
			if err != nil {
				log.Error("removing incoming volume during recovery",
					"volume", volume.ID, "error", err)
				continue
			}
			log.Info("removed incoming volume during recovery", "volume", volume.ID)

		case StatePushing:
			// safe, the commit was never sent
			err = AbortPush(ctx, client, cfg, volume.ID)
			if err != nil {
				log.Error("reverting pushing volume during recovery",
					"volume", volume.ID, "error", err)
				continue
			}
			log.Info("reverted pushing volume during recovery", "volume", volume.ID)

		case StateCommitting:
			// commit outcome unknown, the resolver settles it
			log.Info("committing volume awaits push resolution",
				"volume", volume.ID, "target", volume.PushTarget)

		case StateReady, StateRetired:

		case StateUnknown:
			log.Warn("volume not covered by recovery",
				"volume", volume.ID, "state", string(volume.State))
		}
	}

	return nil
}
