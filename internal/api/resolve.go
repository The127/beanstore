package api

import (
	"context"
	"errors"
	"time"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/logging"
	"github.com/The127/beanstore/internal/storage"
)

const resolveRetryDelay = 30 * time.Second

// resolveAttemptTimeout bounds one query to the target.
const resolveAttemptTimeout = 30 * time.Second

var errNeverCommitted = errors.New("the commit never landed on the target")

// resolveRecovered settles the COMMITTING volumes a restart left
// behind. Each volume resolves on its own retry loop, recovery has
// already handled every other state.
func (s *volumeServiceServer) resolveRecovered() {
	volumes, err := storage.ListVolumes(s.background, s.lvm, s.cfg)
	if err != nil {
		logging.FromContext(s.background).Error("scanning for committing volumes", "error", err)

		return
	}

	for _, volume := range volumes {
		if volume.State != storage.StateCommitting {
			continue
		}
		if volume.PushTarget == "" || volume.Transfer == "" {
			logging.FromContext(s.background).Error("committing volume misses its push tags, resolve manually",
				"volume", volume.ID)

			continue
		}

		go s.resolveUntilSettled(volume.ID, volume.Transfer, volume.PushTarget)
	}
}

func (s *volumeServiceServer) resolveUntilSettled(id, transferID, target string) {
	for {
		if s.tryResolve(id, transferID, target) {
			return
		}

		select {
		case <-s.background.Done():
			return

		case <-time.After(s.resolveRetryDelay):
		}
	}
}

// tryResolve reports whether the target answered and the volume is
// settled.
func (s *volumeServiceServer) tryResolve(id, transferID, target string) bool {
	log := logging.FromContext(s.background)

	conn, err := s.dial(target)
	if err != nil {
		log.Error("dialing push target", "volume", id, "target", target, "error", err)

		return false
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(s.background, resolveAttemptTimeout)
	defer cancel()
	response, err := beanstorev1.NewVolumeServiceClient(conn).ListVolumes(ctx,
		&beanstorev1.ListVolumesRequest{})
	if err != nil {
		log.Warn("push target unreachable, resolution retries",
			"volume", id, "target", target, "error", err)

		return false
	}

	// no operation belongs to a recovered push, the ops calls no-op
	s.settlePush(conn, response.Volumes, id, transferID, "", errNeverCommitted)

	return true
}
