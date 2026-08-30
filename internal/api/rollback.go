package api

import (
	"context"
	"errors"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/logging"
	"github.com/The127/beanstore/internal/storage"
)

// nameReservations holds lv names claimed by an in-flight rollback,
// so the creating verbs cannot take an id while it is briefly
// unclaimed in lvm. Volatile like pins and operation handles.
type nameReservations struct {
	mu    sync.Mutex
	names map[string]bool
}

func newNameReservations() *nameReservations {
	return &nameReservations{names: map[string]bool{}}
}

// Reserve claims all names or none.
func (r *nameReservations) Reserve(names ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, name := range names {
		if r.names[name] {
			return false
		}
	}
	for _, name := range names {
		r.names[name] = true
	}

	return true
}

func (r *nameReservations) Release(names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, name := range names {
		delete(r.names, name)
	}
}

// Reserved reports whether any of the names is claimed.
func (r *nameReservations) Reserved(names ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, name := range names {
		if r.names[name] {
			return true
		}
	}

	return false
}

func (s *volumeServiceServer) RollbackVolume(ctx context.Context, request *beanstorev1.RollbackVolumeRequest) (*beanstorev1.RollbackVolumeResponse, error) {
	if !volumeIDPattern.MatchString(request.VolumeId) {
		return nil, status.Error(codes.InvalidArgument, "volume_id is not a valid lv name")
	}
	if !volumeIDPattern.MatchString(request.SourceSnapshotId) {
		return nil, status.Error(codes.InvalidArgument, "source_snapshot_id is not a valid lv name")
	}

	temp := storage.RollbackTempName(request.VolumeId)
	if !s.reserved.Reserve(request.VolumeId, temp) {
		return nil, status.Error(codes.Aborted, "a rollback of this volume is in progress")
	}
	defer s.reserved.Release(request.VolumeId, temp)

	// leftovers from an earlier failed rollback settle first
	err := s.sweepRollback(ctx, request.VolumeId, temp)
	if err == nil {
		err = s.sweepRenamedRollback(ctx, request.VolumeId)
	}
	if err != nil {
		return nil, volumeError(ctx, err, "rollback failed")
	}

	err = storage.BeginRollback(ctx, s.lvm, s.cfg, request.VolumeId, request.SourceSnapshotId)
	if err != nil {
		return nil, volumeError(ctx, err, "rollback failed")
	}

	err = storage.RemoveVolume(ctx, s.lvm, s.cfg, request.VolumeId)
	if err != nil {
		// before the point of no return, undo the copy
		removeErr := storage.RemoveVolume(ctx, s.lvm, s.cfg, temp)
		if removeErr != nil {
			logging.FromContext(ctx).Error("removing aborted rollback copy",
				"volume", request.VolumeId, "error", removeErr)
		}

		return nil, volumeError(ctx, err, "rollback failed")
	}

	// past the point of no return, the finish must land
	err = s.finishRollback(ctx, temp, request.VolumeId)
	if err != nil {
		logging.FromContext(ctx).Error("finishing rollback",
			"volume", request.VolumeId, "error", err)

		return nil, status.Error(codes.Internal,
			"rollback interrupted, the next rollback call or restart finishes it")
	}

	return &beanstorev1.RollbackVolumeResponse{}, nil
}

// sweepRollback settles a leftover rollback copy without waiting for
// a restart, applying the recovery rules inline.
func (s *volumeServiceServer) sweepRollback(ctx context.Context, volumeID, temp string) error {
	leftover, err := storage.GetVolume(ctx, s.lvm, s.cfg, temp)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if leftover.State != storage.StateRollback || leftover.RollbackTarget != volumeID {
		// a caller minted lv owns the name, BeginRollback will refuse
		return nil
	}

	exists, err := storage.VolumeExists(ctx, s.lvm, s.cfg, volumeID)
	if err != nil {
		return err
	}
	if exists {
		return storage.RemoveVolume(ctx, s.lvm, s.cfg, temp)
	}

	return s.finishRollback(ctx, temp, volumeID)
}

// sweepRenamedRollback settles a copy that already sits under the
// target name and only misses its retag.
func (s *volumeServiceServer) sweepRenamedRollback(ctx context.Context, volumeID string) error {
	current, err := storage.GetVolume(ctx, s.lvm, s.cfg, volumeID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.State != storage.StateRollback || current.RollbackTarget != volumeID {
		return nil
	}

	return s.finishRollback(ctx, volumeID, volumeID)
}

// finishRollback retries the finish, re-detecting whether the rename
// already landed. A failure here strands the volume id until the next
// sweep or restart.
func (s *volumeServiceServer) finishRollback(ctx context.Context, temp, volumeID string) error {
	var err error
	for range 3 {
		name := temp
		if temp != volumeID {
			exists, lookErr := storage.VolumeExists(ctx, s.lvm, s.cfg, temp)
			if lookErr != nil {
				err = lookErr

				continue
			}
			if !exists {
				name = volumeID
			}
		}

		err = storage.FinishRollback(ctx, s.lvm, s.cfg, name, volumeID)
		if err == nil {
			return nil
		}
	}

	return err
}
