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
// does not survive a reboot. Per volume failures are logged and leave
// the volume in its state, the remaining volumes still recover.
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

		case StateReady, StateSnapshot:

		case StatePushing, StateIncoming, StateRetired, StateUnknown:
			log.Warn("volume not covered by recovery",
				"volume", volume.ID, "state", string(volume.State))
		}
	}

	return nil
}
