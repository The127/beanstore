package api

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/logging"
	"github.com/The127/beanstore/internal/storage"
	"github.com/The127/beanstore/lvm"
)

// pushAttempts bounds the stream and commit retries of one push.
const pushAttempts = 5

const pushRetryDelay = 2 * time.Second

// targetAddressPattern is host:port in the lvm tag charset, the
// address persists as a tag.
var targetAddressPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+:[0-9]+$`)

func dialTarget(target string) (*grpc.ClientConn, error) {
	return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func (s *volumeServiceServer) PushVolume(ctx context.Context, request *beanstorev1.PushVolumeRequest) (*beanstorev1.PushVolumeResponse, error) {
	if !volumeIDPattern.MatchString(request.VolumeId) {
		return nil, status.Error(codes.InvalidArgument, "volume_id is not a valid lv name")
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

	err = storage.MarkPushing(ctx, s.lvm, s.cfg, request.VolumeId, request.TransferId, request.TargetAddress)
	if err != nil {
		s.ops.Fail(request.OperationId, err.Error())
		return nil, volumeError(ctx, err, "pushing failed")
	}

	go s.runPush(request.VolumeId, request.TransferId, request.TargetAddress, request.OperationId)

	return &beanstorev1.PushVolumeResponse{}, nil
}

func (s *volumeServiceServer) runPush(id, transferID, target, operationID string) {
	conn, err := s.dial(target)
	if err != nil {
		s.failPush(id, operationID, fmt.Errorf("dialing %s: %w", target, err))
		return
	}
	defer func() { _ = conn.Close() }()
	transfers := beanstorev1.NewTransferServiceClient(conn)

	digest, err := s.streamVolume(transfers, id, transferID, operationID)
	if err != nil {
		s.abortTransfer(transfers, transferID)
		s.failPush(id, operationID, err)

		return
	}

	err = storage.MarkCommitting(s.background, s.lvm, s.cfg, id)
	if err != nil {
		s.abortTransfer(transfers, transferID)
		s.failPush(id, operationID, err)

		return
	}

	s.finishPush(conn, id, transferID, operationID, digest)
}

// streamVolume activates the volume and streams its frames, resuming
// at the queried offset after a dropped stream.
func (s *volumeServiceServer) streamVolume(transfers beanstorev1.TransferServiceClient, id, transferID, operationID string) ([]byte, error) {
	err := s.lvm.ActivateLogicalVolume(s.background,
		lvm.Name(s.cfg.VolumeGroup+"/"+id), lvm.ActivateLogicalVolumeOptions{})
	if err != nil {
		return nil, fmt.Errorf("activating volume %s: %w", id, err)
	}

	volume, err := storage.GetVolume(s.background, s.lvm, s.cfg, id)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := range pushAttempts {
		if attempt > 0 {
			time.Sleep(s.pushRetryDelay)
		}

		digest, err := s.streamOnce(transfers, volume.Path, transferID, operationID)
		if err == nil {
			return digest, nil
		}
		if definitivePushError(err) {
			return nil, err
		}
		lastErr = err
		logging.FromContext(s.background).Warn("push stream attempt failed",
			"volume", id, "attempt", attempt+1, "error", err)
	}

	return nil, lastErr
}

func (s *volumeServiceServer) streamOnce(transfers beanstorev1.TransferServiceClient, path, transferID, operationID string) ([]byte, error) {
	offsets, err := transfers.QueryTransfer(s.background,
		&beanstorev1.QueryTransferRequest{TransferId: transferID})
	if err != nil {
		return nil, fmt.Errorf("querying transfer: %w", err)
	}

	stream, err := transfers.Receive(s.background)
	if err != nil {
		return nil, fmt.Errorf("opening stream: %w", err)
	}

	// a failed Send hides the cause, CloseAndRecv surfaces it
	sendFailed := false
	send := func(request *beanstorev1.ReceiveRequest) error {
		err := stream.Send(request)
		if err != nil {
			sendFailed = true
		}

		return err
	}

	err = send(&beanstorev1.ReceiveRequest{Content: &beanstorev1.ReceiveRequest_Header{
		Header: &beanstorev1.ReceiveHeader{TransferId: transferID},
	}})

	var digest []byte
	if err == nil {
		_, digest, err = storage.ReadDevice(s.background, path, func(frame storage.Frame) error {
			if frame.Offset < offsets.NextOffset {
				return nil
			}

			err := send(&beanstorev1.ReceiveRequest{Content: &beanstorev1.ReceiveRequest_Frame{
				Frame: &beanstorev1.Frame{Offset: frame.Offset, Data: frame.Data},
			}})
			if err != nil {
				return err
			}
			s.ops.Progress(operationID, frame.Offset+uint64(len(frame.Data)))

			return nil
		})
	}
	if err != nil {
		if sendFailed {
			_, recvErr := stream.CloseAndRecv()
			if recvErr != nil {
				return nil, fmt.Errorf("streaming frames: %w", recvErr)
			}
		}

		return nil, fmt.Errorf("streaming frames: %w", err)
	}

	_, err = stream.CloseAndRecv()
	if err != nil {
		return nil, fmt.Errorf("closing stream: %w", err)
	}

	return digest, nil
}

// finishPush drives a COMMITTING volume to its outcome. The commit is
// idempotent and retried, a dead transfer or exhausted retries fall
// back to resolving by the volume's state on the target.
func (s *volumeServiceServer) finishPush(conn *grpc.ClientConn, id, transferID, operationID string, digest []byte) {
	transfers := beanstorev1.NewTransferServiceClient(conn)

	var lastErr error
	for attempt := range pushAttempts {
		if attempt > 0 {
			time.Sleep(s.pushRetryDelay)
		}

		_, err := transfers.CommitTransfer(s.background,
			&beanstorev1.CommitTransferRequest{TransferId: transferID, Digest: digest})
		if err == nil {
			s.retirePushed(id, operationID)

			return
		}
		if status.Code(err) == codes.DataLoss {
			s.failPush(id, operationID, err)

			return
		}
		lastErr = err
		if status.Code(err) == codes.NotFound {
			break
		}
	}

	s.resolvePush(conn, id, transferID, operationID, lastErr)
}

// resolvePush settles a COMMITTING volume by the volume's state on
// the target: present and not INCOMING means the commit landed. An
// unreachable target leaves the volume COMMITTING.
func (s *volumeServiceServer) resolvePush(conn *grpc.ClientConn, id, transferID, operationID string, commitErr error) {
	response, err := beanstorev1.NewVolumeServiceClient(conn).ListVolumes(s.background,
		&beanstorev1.ListVolumesRequest{})
	if err != nil {
		deactivateErr := s.lvm.DeactivateLogicalVolume(s.background,
			lvm.Name(s.cfg.VolumeGroup+"/"+id), lvm.DeactivateLogicalVolumeOptions{})
		if deactivateErr != nil {
			logging.FromContext(s.background).Error("deactivating unresolved volume",
				"volume", id, "error", deactivateErr)
		}
		logging.FromContext(s.background).Error("push commit outcome unknown",
			"volume", id, "error", err)
		s.ops.Fail(operationID, fmt.Sprintf("commit outcome unknown: %v", commitErr))

		return
	}

	for _, volume := range response.Volumes {
		if volume.VolumeId == id && volume.State != beanstorev1.VolumeState_VOLUME_STATE_INCOMING {
			s.retirePushed(id, operationID)

			return
		}
	}

	// nothing committed, make sure nothing still can
	s.abortTransfer(beanstorev1.NewTransferServiceClient(conn), transferID)
	s.failPush(id, operationID, commitErr)
}

func (s *volumeServiceServer) retirePushed(id, operationID string) {
	err := storage.RetirePushed(s.background, s.lvm, s.cfg, id)
	if err != nil {
		logging.FromContext(s.background).Error("retiring pushed volume", "volume", id, "error", err)
		s.ops.Fail(operationID, err.Error())

		return
	}

	s.ops.Done(operationID)
}

// failPush reverts the volume to READY and fails the operation.
func (s *volumeServiceServer) failPush(id, operationID string, cause error) {
	logging.FromContext(s.background).Error("pushing volume", "volume", id, "error", cause)

	err := storage.AbortPush(s.background, s.lvm, s.cfg, id)
	if err != nil {
		logging.FromContext(s.background).Error("aborting push", "volume", id, "error", err)
	}

	s.ops.Fail(operationID, cause.Error())
}

func (s *volumeServiceServer) abortTransfer(transfers beanstorev1.TransferServiceClient, transferID string) {
	_, err := transfers.AbortTransfer(s.background,
		&beanstorev1.AbortTransferRequest{TransferId: transferID})
	if err != nil {
		logging.FromContext(s.background).Warn("aborting transfer on target",
			"transfer", transferID, "error", err)
	}
}

// definitivePushError reports refusals a retry cannot change.
func definitivePushError(err error) bool {
	switch status.Code(err) {
	case codes.NotFound, codes.InvalidArgument, codes.AlreadyExists,
		codes.FailedPrecondition, codes.DataLoss:
		return true

	default:
		return false
	}
}
