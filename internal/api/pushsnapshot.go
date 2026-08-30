package api

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/logging"
	"github.com/The127/beanstore/internal/storage"
)

func (s *volumeServiceServer) PushSnapshot(ctx context.Context, request *beanstorev1.PushSnapshotRequest) (*beanstorev1.PushSnapshotResponse, error) {
	if !volumeIDPattern.MatchString(request.SnapshotId) {
		return nil, status.Error(codes.InvalidArgument, "snapshot_id is not a valid lv name")
	}
	if !volumeIDPattern.MatchString(request.TransferId) {
		return nil, status.Error(codes.InvalidArgument, "transfer_id is not a valid tag value")
	}
	if !targetAddressPattern.MatchString(request.TargetAddress) {
		return nil, status.Error(codes.InvalidArgument, "target_address must be host:port")
	}
	if request.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument, "operation_id must be set")
	}

	err := s.ops.Begin(request.OperationId)
	if err != nil {
		return nil, status.Error(codes.AlreadyExists, "operation id already used")
	}

	// pin before the state check, like Export, so a racing delete
	// cannot slip between them
	s.pins.Acquire(request.SnapshotId)

	snapshot, err := storage.GetVolume(ctx, s.lvm, s.cfg, request.SnapshotId)
	if err == nil && snapshot.State != storage.StateSnapshot {
		err = &storage.WrongStateError{Volume: request.SnapshotId, Found: snapshot.State}
	}
	if err != nil {
		s.releaseExport(request.SnapshotId)
		s.ops.Fail(request.OperationId, err.Error())

		return nil, volumeError(ctx, err, "pushing snapshot failed")
	}

	go s.runSnapshotPush(request.SnapshotId, request.TransferId, request.TargetAddress, request.OperationId)

	return &beanstorev1.PushSnapshotResponse{}, nil
}

// runSnapshotPush copies the snapshot to the target. No source state
// changes, the pin alone protects the snapshot while it streams.
func (s *volumeServiceServer) runSnapshotPush(id, transferID, target, operationID string) {
	defer s.releaseExport(id)

	conn, err := s.dial(target)
	if err != nil {
		s.failSnapshotPush(operationID, id, fmt.Errorf("dialing %s: %w", target, err))

		return
	}
	defer func() { _ = conn.Close() }()
	transfers := beanstorev1.NewTransferServiceClient(conn)

	digest, err := s.streamVolume(transfers, id, transferID, operationID, true)
	if err != nil {
		s.abortTransfer(transfers, transferID)
		s.failSnapshotPush(operationID, id, err)

		return
	}

	outcome, err := s.commitPush(transfers, transferID, digest)
	switch outcome {
	case commitLanded:
		s.ops.Done(operationID)

	case commitRefused:
		s.failSnapshotPush(operationID, id, err)

	case commitUnknown:
		// aborting any live session makes the failure answer final, a
		// commit that landed anyway leaves a stray target volume for
		// the orchestrator
		s.abortTransfer(transfers, transferID)
		s.failSnapshotPush(operationID, id, fmt.Errorf("commit outcome unknown: %w", err))
	}
}

func (s *volumeServiceServer) failSnapshotPush(operationID, id string, cause error) {
	logging.FromContext(s.background).Error("pushing snapshot", "snapshot", id, "error", cause)
	s.ops.Fail(operationID, cause.Error())
}
