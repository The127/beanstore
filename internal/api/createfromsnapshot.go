package api

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/logging"
	"github.com/The127/beanstore/internal/storage"
	"github.com/The127/beanstore/lvm"
)

func (s *volumeServiceServer) CreateVolumeFromSnapshot(ctx context.Context, request *beanstorev1.CreateVolumeFromSnapshotRequest) (*beanstorev1.CreateVolumeFromSnapshotResponse, error) {
	if !volumeIDPattern.MatchString(request.VolumeId) {
		return nil, status.Error(codes.InvalidArgument, "volume_id is not a valid lv name")
	}
	if !volumeIDPattern.MatchString(request.SourceSnapshotId) {
		return nil, status.Error(codes.InvalidArgument, "source_snapshot_id is not a valid lv name")
	}
	if request.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument, "operation_id must be set")
	}

	err := s.ops.Begin(request.OperationId)
	if err != nil {
		return nil, status.Error(codes.AlreadyExists, "operation id already used")
	}

	// pin before the state check, like Export, so a racing delete
	// cannot slip between
	s.pins.Acquire(request.SourceSnapshotId)

	snapshot, err := storage.GetVolume(ctx, s.lvm, s.cfg, request.SourceSnapshotId)
	if err == nil && snapshot.State != storage.StateSnapshot {
		err = &storage.WrongStateError{Volume: request.SourceSnapshotId, Found: snapshot.State}
	}
	if err != nil {
		s.releaseExport(request.SourceSnapshotId)
		s.ops.Fail(request.OperationId, err.Error())

		return nil, volumeError(ctx, err, "creating volume from snapshot failed")
	}

	exists, err := storage.VolumeExists(ctx, s.lvm, s.cfg, request.VolumeId)
	if err != nil {
		s.releaseExport(request.SourceSnapshotId)
		s.ops.Fail(request.OperationId, err.Error())
		logging.FromContext(ctx).Error("looking up volume", "volume", request.VolumeId, "error", err)

		return nil, status.Error(codes.Internal, "volume lookup failed")
	}
	if exists {
		s.releaseExport(request.SourceSnapshotId)
		s.ops.Fail(request.OperationId, "volume already exists")

		return nil, status.Error(codes.AlreadyExists, "volume already exists")
	}

	go s.runCopyFromSnapshot(request.VolumeId, request.SourceSnapshotId, request.OperationId, snapshot.SizeBytes)

	return &beanstorev1.CreateVolumeFromSnapshotResponse{}, nil
}

func (s *volumeServiceServer) runCopyFromSnapshot(volumeID, snapshotID, operationID string, sizeBytes uint64) {
	defer s.releaseExport(snapshotID)
	ctx := s.background

	err := s.copyFromSnapshot(ctx, volumeID, snapshotID, operationID, sizeBytes)
	if err != nil {
		logging.FromContext(ctx).Error("creating volume from snapshot",
			"volume", volumeID, "snapshot", snapshotID, "error", err)
		s.ops.Fail(operationID, err.Error())

		// remove the half-made target now instead of at the next boot
		removeErr := storage.RemoveVolume(ctx, s.lvm, s.cfg, volumeID)
		if removeErr != nil && !errors.Is(removeErr, lvm.ErrNotFound) {
			logging.FromContext(ctx).Error("removing failed copy", "volume", volumeID, "error", removeErr)
		}

		return
	}

	s.ops.Done(operationID)
}

func (s *volumeServiceServer) copyFromSnapshot(ctx context.Context, volumeID, snapshotID, operationID string, sizeBytes uint64) error {
	// born active for writing, the creating tag covers a crash
	err := s.lvm.CreateThinVolume(ctx, s.cfg.VolumeGroup, s.cfg.ThinPool, volumeID, sizeBytes,
		lvm.CreateThinVolumeOptions{
			AddTags: []string{storage.StateTag(storage.StateCreating)},
		})
	if err != nil {
		return fmt.Errorf("creating copy target %s: %w", volumeID, err)
	}

	target, err := storage.GetVolume(ctx, s.lvm, s.cfg, volumeID)
	if err != nil {
		return err
	}

	err = s.lvm.ActivateLogicalVolume(ctx, lvm.Name(s.cfg.VolumeGroup+"/"+snapshotID),
		lvm.ActivateLogicalVolumeOptions{IgnoreActivationSkip: true})
	if err != nil {
		return fmt.Errorf("activating snapshot %s: %w", snapshotID, err)
	}

	snapshot, err := storage.GetVolume(ctx, s.lvm, s.cfg, snapshotID)
	if err != nil {
		return err
	}

	err = storage.CopyDevice(ctx, snapshot.Path, target.Path, func(done uint64) {
		s.ops.Progress(operationID, done)
	})
	if err != nil {
		return err
	}

	err = s.lvm.DeactivateLogicalVolume(ctx, lvm.Name(s.cfg.VolumeGroup+"/"+volumeID),
		lvm.DeactivateLogicalVolumeOptions{})
	if err != nil {
		return fmt.Errorf("deactivating copy %s: %w", volumeID, err)
	}

	err = s.lvm.ChangeLogicalVolume(ctx, lvm.Name(s.cfg.VolumeGroup+"/"+volumeID),
		lvm.ChangeLogicalVolumeOptions{
			AddTags:    []string{storage.StateTag(storage.StateReady)},
			RemoveTags: []string{storage.StateTag(storage.StateCreating)},
		})
	if err != nil {
		return fmt.Errorf("readying copy %s: %w", volumeID, err)
	}

	return nil
}
