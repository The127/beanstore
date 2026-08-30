package api

import (
	"context"
	"errors"
	"io"
	"regexp"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/internal/logging"
	"github.com/The127/beanstore/internal/operations"
	"github.com/The127/beanstore/internal/storage"
	"github.com/The127/beanstore/lvm"
)

type volumeServiceServer struct {
	beanstorev1.UnimplementedVolumeServiceServer
	lvm       *lvm.Client
	cfg       config.Config
	ops       *operations.Table
	pins      *storage.ExportPins
	transfers *storage.Transfers
	// background outlives requests, long-running operations run on it.
	background context.Context
	// dial connects to a push target, tests replace it.
	dial func(target string) (*grpc.ClientConn, error)
	// pushRetryDelay spaces the retries of one push.
	pushRetryDelay time.Duration
	// resolveRetryDelay spaces the resolution attempts of recovered
	// COMMITTING volumes.
	resolveRetryDelay time.Duration
	// reserved holds names an in-flight rollback claims.
	reserved *nameReservations
}

type operationServiceServer struct {
	beanstorev1.UnimplementedOperationServiceServer
	ops *operations.Table
}

type transferServiceServer struct {
	beanstorev1.UnimplementedTransferServiceServer
	transfers *storage.Transfers
}

// Register wires all beanstore services onto the given grpc server.
// The context must outlive the server, long-running operations run on
// it.
func Register(ctx context.Context, server *grpc.Server, client *lvm.Client, cfg config.Config) error {
	dialCreds, err := clientCredentials(cfg)
	if err != nil {
		return err
	}

	ops := operations.NewTable()
	transfers := storage.NewTransfers(ctx, client, cfg)
	volumes := &volumeServiceServer{
		lvm: client, cfg: cfg, ops: ops, pins: storage.NewExportPins(),
		transfers: transfers, background: ctx,
		dial: func(target string) (*grpc.ClientConn, error) {
			return grpc.NewClient(target, grpc.WithTransportCredentials(dialCreds))
		},
		pushRetryDelay: pushRetryDelay, resolveRetryDelay: resolveRetryDelay,
		reserved: newNameReservations(),
	}
	beanstorev1.RegisterVolumeServiceServer(server, volumes)
	beanstorev1.RegisterOperationServiceServer(server, &operationServiceServer{ops: ops})
	beanstorev1.RegisterTransferServiceServer(server, &transferServiceServer{transfers: transfers})

	go volumes.resolveRecovered()

	return nil
}

// volumeIDPattern is the lv name charset, minus a leading dash or dot.
var volumeIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_+][a-zA-Z0-9_+.-]*$`)

func (s *volumeServiceServer) CreateVolume(ctx context.Context, request *beanstorev1.CreateVolumeRequest) (*beanstorev1.CreateVolumeResponse, error) {
	if !volumeIDPattern.MatchString(request.VolumeId) {
		return nil, status.Error(codes.InvalidArgument, "volume_id is not a valid lv name")
	}
	if request.SizeBytes == 0 {
		return nil, status.Error(codes.InvalidArgument, "size_bytes must be set")
	}
	if request.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument, "operation_id must be set")
	}

	exists, err := storage.VolumeExists(ctx, s.lvm, s.cfg, request.VolumeId)
	if err != nil {
		logging.FromContext(ctx).Error("looking up volume", "volume", request.VolumeId, "error", err)
		return nil, status.Error(codes.Internal, "volume lookup failed")
	}
	if exists || s.reserved.Reserved(request.VolumeId) {
		return nil, status.Error(codes.AlreadyExists, "volume already exists")
	}

	err = s.ops.Begin(request.OperationId)
	if err != nil {
		return nil, status.Error(codes.AlreadyExists, "operation id already used")
	}

	go s.runCreate(request.VolumeId, request.SizeBytes, request.OperationId)

	return &beanstorev1.CreateVolumeResponse{}, nil
}

func (s *volumeServiceServer) runCreate(id string, sizeBytes uint64, operationID string) {
	err := storage.CreateVolume(s.background, s.lvm, s.cfg, id, sizeBytes)
	if err != nil {
		logging.FromContext(s.background).Error("creating volume", "volume", id, "error", err)
		s.ops.Fail(operationID, err.Error())

		return
	}

	s.ops.Done(operationID)
}

func (s *operationServiceServer) GetOperation(_ context.Context, request *beanstorev1.GetOperationRequest) (*beanstorev1.GetOperationResponse, error) {
	operation, exists := s.ops.Get(request.OperationId)
	if !exists {
		return nil, status.Error(codes.NotFound, "unknown operation")
	}

	response := &beanstorev1.GetOperationResponse{}
	switch operation.Phase {
	case operations.Pending:
		response.State = &beanstorev1.GetOperationResponse_Pending{Pending: &beanstorev1.OperationPending{}}

	case operations.Progressing:
		response.State = &beanstorev1.GetOperationResponse_Progress{Progress: &beanstorev1.OperationProgress{
			BytesDone: operation.BytesDone,
		}}

	case operations.Done:
		response.State = &beanstorev1.GetOperationResponse_Done{Done: &beanstorev1.OperationDone{}}

	case operations.Failed:
		response.State = &beanstorev1.GetOperationResponse_Failed{Failed: &beanstorev1.OperationFailed{
			Reason: operation.Reason,
		}}
	}

	return response, nil
}

func (s *volumeServiceServer) ListVolumes(ctx context.Context, _ *beanstorev1.ListVolumesRequest) (*beanstorev1.ListVolumesResponse, error) {
	volumes, err := storage.ListVolumes(ctx, s.lvm, s.cfg)
	if err != nil {
		logging.FromContext(ctx).Error("listing volumes", "error", err)
		return nil, status.Error(codes.Internal, "scanning volumes failed")
	}

	response := &beanstorev1.ListVolumesResponse{}
	for _, volume := range volumes {
		response.Volumes = append(response.Volumes, &beanstorev1.Volume{
			VolumeId:  volume.ID,
			State:     protoState(volume.State),
			SizeBytes: volume.SizeBytes,
			UsedBytes: volume.UsedBytes,
			OriginId:  volume.Origin,
		})
	}

	return response, nil
}

func (s *volumeServiceServer) Attach(ctx context.Context, request *beanstorev1.AttachRequest) (*beanstorev1.AttachResponse, error) {
	if !volumeIDPattern.MatchString(request.VolumeId) {
		return nil, status.Error(codes.InvalidArgument, "volume_id is not a valid lv name")
	}

	path, err := storage.Attach(ctx, s.lvm, s.cfg, request.VolumeId)
	if err != nil {
		return nil, volumeError(ctx, err, "attaching failed")
	}

	return &beanstorev1.AttachResponse{DevicePath: path}, nil
}

func (s *volumeServiceServer) Detach(ctx context.Context, request *beanstorev1.DetachRequest) (*beanstorev1.DetachResponse, error) {
	if !volumeIDPattern.MatchString(request.VolumeId) {
		return nil, status.Error(codes.InvalidArgument, "volume_id is not a valid lv name")
	}

	err := storage.Detach(ctx, s.lvm, s.cfg, request.VolumeId)
	if err != nil {
		return nil, volumeError(ctx, err, "detaching failed")
	}

	return &beanstorev1.DetachResponse{}, nil
}

func (s *volumeServiceServer) DeleteVolume(ctx context.Context, request *beanstorev1.DeleteVolumeRequest) (*beanstorev1.DeleteVolumeResponse, error) {
	if !volumeIDPattern.MatchString(request.VolumeId) {
		return nil, status.Error(codes.InvalidArgument, "volume_id is not a valid lv name")
	}
	if request.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument, "operation_id must be set")
	}

	err := s.ops.Begin(request.OperationId)
	if err != nil {
		return nil, status.Error(codes.AlreadyExists, "operation id already used")
	}

	snapshots, err := storage.MarkDeleting(ctx, s.lvm, s.cfg, s.pins, request.VolumeId, request.Force)
	if err != nil {
		s.ops.Fail(request.OperationId, err.Error())
		return nil, volumeError(ctx, err, "deleting failed")
	}

	go s.runDelete(request.VolumeId, snapshots, request.OperationId)

	return &beanstorev1.DeleteVolumeResponse{}, nil
}

func (s *volumeServiceServer) runDelete(id string, snapshots []string, operationID string) {
	for _, target := range append(snapshots, id) {
		err := storage.RemoveVolume(s.background, s.lvm, s.cfg, target)
		if err != nil {
			logging.FromContext(s.background).Error("removing volume", "volume", target, "error", err)
			s.ops.Fail(operationID, err.Error())

			return
		}
	}

	s.ops.Done(operationID)
}

func (s *volumeServiceServer) CreateSnapshot(ctx context.Context, request *beanstorev1.CreateSnapshotRequest) (*beanstorev1.CreateSnapshotResponse, error) {
	if !volumeIDPattern.MatchString(request.VolumeId) {
		return nil, status.Error(codes.InvalidArgument, "volume_id is not a valid lv name")
	}
	if !volumeIDPattern.MatchString(request.SnapshotId) {
		return nil, status.Error(codes.InvalidArgument, "snapshot_id is not a valid lv name")
	}

	exists, err := storage.VolumeExists(ctx, s.lvm, s.cfg, request.SnapshotId)
	if err != nil {
		logging.FromContext(ctx).Error("looking up snapshot", "snapshot", request.SnapshotId, "error", err)
		return nil, status.Error(codes.Internal, "snapshot lookup failed")
	}
	if exists {
		return nil, status.Error(codes.AlreadyExists, "snapshot already exists")
	}

	err = storage.CreateSnapshot(ctx, s.lvm, s.cfg, request.VolumeId, request.SnapshotId)
	if err != nil {
		return nil, volumeError(ctx, err, "creating snapshot failed")
	}

	return &beanstorev1.CreateSnapshotResponse{}, nil
}

func (s *volumeServiceServer) DeleteSnapshot(ctx context.Context, request *beanstorev1.DeleteSnapshotRequest) (*beanstorev1.DeleteSnapshotResponse, error) {
	if !volumeIDPattern.MatchString(request.SnapshotId) {
		return nil, status.Error(codes.InvalidArgument, "snapshot_id is not a valid lv name")
	}

	err := storage.DeleteSnapshot(ctx, s.lvm, s.cfg, s.pins, request.SnapshotId)
	if err != nil {
		return nil, volumeError(ctx, err, "deleting snapshot failed")
	}

	return &beanstorev1.DeleteSnapshotResponse{}, nil
}

func (s *volumeServiceServer) ResizeVolume(ctx context.Context, request *beanstorev1.ResizeVolumeRequest) (*beanstorev1.ResizeVolumeResponse, error) {
	if !volumeIDPattern.MatchString(request.VolumeId) {
		return nil, status.Error(codes.InvalidArgument, "volume_id is not a valid lv name")
	}
	if request.SizeBytes == 0 {
		return nil, status.Error(codes.InvalidArgument, "size_bytes must be set")
	}

	err := storage.ResizeVolume(ctx, s.lvm, s.cfg, request.VolumeId, request.SizeBytes)
	if err != nil {
		return nil, volumeError(ctx, err, "resizing failed")
	}

	return &beanstorev1.ResizeVolumeResponse{}, nil
}

func (s *volumeServiceServer) Export(request *beanstorev1.ExportRequest, stream grpc.ServerStreamingServer[beanstorev1.ExportResponse]) error {
	if !volumeIDPattern.MatchString(request.SnapshotId) {
		return status.Error(codes.InvalidArgument, "snapshot_id is not a valid lv name")
	}

	ctx := stream.Context()
	s.pins.Acquire(request.SnapshotId)
	defer s.releaseExport(request.SnapshotId)

	snapshot, err := storage.GetVolume(ctx, s.lvm, s.cfg, request.SnapshotId)
	if err != nil {
		return volumeError(ctx, err, "export lookup failed")
	}
	if snapshot.State != storage.StateSnapshot {
		return volumeError(ctx, &storage.WrongStateError{
			Volume: request.SnapshotId, Found: snapshot.State,
		}, "export refused")
	}

	name := lvm.Name(s.cfg.VolumeGroup + "/" + request.SnapshotId)
	err = s.lvm.ActivateLogicalVolume(ctx, name, lvm.ActivateLogicalVolumeOptions{
		IgnoreActivationSkip: true,
	})
	if err != nil {
		logging.FromContext(ctx).Error("activating snapshot for export", "snapshot", request.SnapshotId, "error", err)
		return status.Error(codes.Internal, "activating snapshot failed")
	}

	snapshot, err = storage.GetVolume(ctx, s.lvm, s.cfg, request.SnapshotId)
	if err != nil {
		return volumeError(ctx, err, "export lookup failed")
	}

	size, digest, err := storage.ReadDevice(ctx, snapshot.Path, func(frame storage.Frame) error {
		return stream.Send(&beanstorev1.ExportResponse{
			Content: &beanstorev1.ExportResponse_Frame{Frame: &beanstorev1.Frame{
				Offset: frame.Offset,
				Data:   frame.Data,
			}},
		})
	})
	if err != nil {
		logging.FromContext(ctx).Error("exporting snapshot", "snapshot", request.SnapshotId, "error", err)
		return status.Error(codes.Internal, "export failed")
	}

	return stream.Send(&beanstorev1.ExportResponse{
		Content: &beanstorev1.ExportResponse_Trailer{Trailer: &beanstorev1.ExportTrailer{
			Digest:    digest,
			SizeBytes: size,
		}},
	})
}

// releaseExport unpins the snapshot and deactivates it after the last
// export. The device may already be gone or still in use by a racing
// export, both are fine.
func (s *volumeServiceServer) releaseExport(id string) {
	if !s.pins.Release(id) {
		return
	}

	err := s.lvm.DeactivateLogicalVolume(s.background,
		lvm.Name(s.cfg.VolumeGroup+"/"+id), lvm.DeactivateLogicalVolumeOptions{})
	if err != nil && !errors.Is(err, lvm.ErrInUse) && !errors.Is(err, lvm.ErrNotFound) {
		logging.FromContext(s.background).Error("deactivating exported snapshot", "snapshot", id, "error", err)
	}
}

func (s *volumeServiceServer) PrepareReceive(ctx context.Context, request *beanstorev1.PrepareReceiveRequest) (*beanstorev1.PrepareReceiveResponse, error) {
	if !volumeIDPattern.MatchString(request.VolumeId) {
		return nil, status.Error(codes.InvalidArgument, "volume_id is not a valid lv name")
	}
	if !volumeIDPattern.MatchString(request.TransferId) {
		return nil, status.Error(codes.InvalidArgument, "transfer_id is not a valid tag value")
	}
	if request.SizeBytes == 0 {
		return nil, status.Error(codes.InvalidArgument, "size_bytes must be set")
	}

	if s.reserved.Reserved(request.VolumeId) {
		return nil, status.Error(codes.AlreadyExists, "volume already exists")
	}

	existing, err := storage.GetVolume(ctx, s.lvm, s.cfg, request.VolumeId)
	switch {
	case err == nil:
		return nil, status.Error(codes.AlreadyExists,
			(&storage.WrongStateError{Volume: request.VolumeId, Found: existing.State}).Error())

	case !errors.Is(err, storage.ErrNotFound):
		return nil, volumeError(ctx, err, "prepare lookup failed")
	}

	err = s.transfers.PrepareReceive(ctx, request.TransferId, request.VolumeId, request.SizeBytes)
	if err != nil {
		return nil, transferError(ctx, err, "preparing transfer failed")
	}

	return &beanstorev1.PrepareReceiveResponse{}, nil
}

func (s *transferServiceServer) QueryTransfer(_ context.Context, request *beanstorev1.QueryTransferRequest) (*beanstorev1.QueryTransferResponse, error) {
	offset, err := s.transfers.NextOffset(request.TransferId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "unknown transfer")
	}

	return &beanstorev1.QueryTransferResponse{NextOffset: offset}, nil
}

func (s *transferServiceServer) Receive(stream grpc.ClientStreamingServer[beanstorev1.ReceiveRequest, beanstorev1.ReceiveResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Error(codes.InvalidArgument, "the stream must start with a header")
	}
	header := first.GetHeader()
	if header == nil {
		return status.Error(codes.InvalidArgument, "the stream must start with a header")
	}

	err = s.transfers.Attach(header.TransferId)
	if err != nil {
		return transferError(stream.Context(), err, "attaching stream failed")
	}
	defer s.transfers.Detach(header.TransferId)

	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&beanstorev1.ReceiveResponse{})
		}
		if err != nil {
			return err
		}

		frame := request.GetFrame()
		if frame == nil {
			return status.Error(codes.InvalidArgument, "expected a frame")
		}

		err = s.transfers.Write(header.TransferId, frame.Offset, frame.Data)
		if err != nil {
			return transferError(stream.Context(), err, "writing frame failed")
		}
	}
}

func (s *transferServiceServer) CommitTransfer(ctx context.Context, request *beanstorev1.CommitTransferRequest) (*beanstorev1.CommitTransferResponse, error) {
	err := s.transfers.Commit(ctx, request.TransferId, request.Digest)
	if err != nil {
		return nil, transferError(ctx, err, "committing transfer failed")
	}

	return &beanstorev1.CommitTransferResponse{}, nil
}

func (s *transferServiceServer) AbortTransfer(_ context.Context, request *beanstorev1.AbortTransferRequest) (*beanstorev1.AbortTransferResponse, error) {
	s.transfers.Abort(request.TransferId)

	return &beanstorev1.AbortTransferResponse{}, nil
}

// transferError maps transfer errors onto grpc codes. Internal
// failures are logged and answered without detail.
func transferError(ctx context.Context, err error, message string) error {
	switch {
	case errors.Is(err, storage.ErrTransferLimit):
		return status.Error(codes.ResourceExhausted, err.Error())

	case errors.Is(err, storage.ErrTransferUsed):
		return status.Error(codes.AlreadyExists, err.Error())

	case errors.Is(err, storage.ErrTransferUnknown):
		return status.Error(codes.NotFound, "unknown transfer")

	case errors.Is(err, storage.ErrTransferBusy):
		return status.Error(codes.Aborted, err.Error())

	case errors.Is(err, storage.ErrBadFrame):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, storage.ErrDigestMismatch):
		return status.Error(codes.DataLoss, err.Error())

	default:
		logging.FromContext(ctx).Error(message, "error", err)
		return status.Error(codes.Internal, message)
	}
}

// volumeError maps storage errors onto grpc codes. Internal failures
// are logged and answered without detail.
func volumeError(ctx context.Context, err error, message string) error {
	var wrongState *storage.WrongStateError
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return status.Error(codes.NotFound, "volume does not exist")

	case errors.Is(err, storage.ErrShrink), errors.Is(err, storage.ErrHasSnapshots):
		return status.Error(codes.FailedPrecondition, err.Error())

	case errors.Is(err, storage.ErrExportInProgress):
		return status.Error(codes.Aborted, err.Error())

	case errors.As(err, &wrongState):
		refusal := status.New(codes.FailedPrecondition, wrongState.Error())
		detailed, detailErr := refusal.WithDetails(&beanstorev1.WrongState{
			VolumeId: wrongState.Volume,
			Found:    protoState(wrongState.Found),
		})
		if detailErr != nil {
			return refusal.Err()
		}

		return detailed.Err()

	case errors.Is(err, storage.ErrWrongLineage):
		return status.Error(codes.FailedPrecondition, err.Error())

	case errors.Is(err, lvm.ErrInUse):
		return status.Error(codes.FailedPrecondition, "volume is in use")

	case errors.Is(err, lvm.ErrNotFound):
		return status.Error(codes.NotFound, "volume does not exist")

	case errors.Is(err, lvm.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, err.Error())

	default:
		logging.FromContext(ctx).Error(message, "error", err)
		return status.Error(codes.Internal, message)
	}
}

func (s *volumeServiceServer) GetNodeStatus(ctx context.Context, _ *beanstorev1.GetNodeStatusRequest) (*beanstorev1.GetNodeStatusResponse, error) {
	nodeStatus, err := storage.GetNodeStatus(ctx, s.lvm, s.cfg)
	if err != nil {
		logging.FromContext(ctx).Error("reading node status", "error", err)
		return nil, status.Error(codes.Internal, "reading node status failed")
	}

	version, err := s.lvm.GetVersion(ctx, lvm.VersionOptions{})
	if err != nil {
		logging.FromContext(ctx).Error("reading lvm version", "error", err)
		return nil, status.Error(codes.Internal, "reading lvm version failed")
	}

	response := &beanstorev1.GetNodeStatusResponse{
		PoolSizeBytes:         nodeStatus.PoolSizeBytes,
		PoolUsedBytes:         nodeStatus.PoolUsedBytes,
		PoolMetadataSizeBytes: nodeStatus.PoolMetadataSizeBytes,
		PoolMetadataUsedBytes: nodeStatus.PoolMetadataUsedBytes,
		CommittedBytes:        nodeStatus.CommittedBytes,
		VolumeCounts:          map[string]uint32{},
		BeanstoreVersion:      beanstoreVersion(),
		LvmVersion:            version.LVM,
	}
	for state, count := range nodeStatus.VolumeCounts {
		response.VolumeCounts[stateName(state)] = count
	}

	return response, nil
}

func stateName(state storage.State) string {
	if state == storage.StateUnknown {
		return "unknown"
	}

	return string(state)
}

func beanstoreVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "unknown"
	}

	return info.Main.Version
}

func protoState(state storage.State) beanstorev1.VolumeState {
	switch state {
	case storage.StateCreating:
		return beanstorev1.VolumeState_VOLUME_STATE_CREATING

	case storage.StateReady:
		return beanstorev1.VolumeState_VOLUME_STATE_READY

	case storage.StateAttached:
		return beanstorev1.VolumeState_VOLUME_STATE_ATTACHED

	case storage.StatePushing:
		return beanstorev1.VolumeState_VOLUME_STATE_PUSHING

	case storage.StateCommitting:
		return beanstorev1.VolumeState_VOLUME_STATE_COMMITTING

	case storage.StateIncoming:
		return beanstorev1.VolumeState_VOLUME_STATE_INCOMING

	case storage.StateRetired:
		return beanstorev1.VolumeState_VOLUME_STATE_RETIRED

	case storage.StateDeleting:
		return beanstorev1.VolumeState_VOLUME_STATE_DELETING

	case storage.StateSnapshot:
		return beanstorev1.VolumeState_VOLUME_STATE_SNAPSHOT

	case storage.StateRollback:
		return beanstorev1.VolumeState_VOLUME_STATE_ROLLBACK

	default:
		return beanstorev1.VolumeState_VOLUME_STATE_UNSPECIFIED
	}
}
