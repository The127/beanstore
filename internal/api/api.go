package api

import (
	"context"
	"errors"
	"regexp"
	"runtime/debug"

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
	lvm *lvm.Client
	cfg config.Config
	ops *operations.Table
	// background outlives requests, long-running operations run on it.
	background context.Context
}

type operationServiceServer struct {
	beanstorev1.UnimplementedOperationServiceServer
	ops *operations.Table
}

// Register wires all beanstore services onto the given grpc server.
// The context must outlive the server, long-running operations run on
// it.
func Register(ctx context.Context, server *grpc.Server, client *lvm.Client, cfg config.Config) {
	ops := operations.NewTable()
	beanstorev1.RegisterVolumeServiceServer(server, &volumeServiceServer{
		lvm: client, cfg: cfg, ops: ops, background: ctx,
	})
	beanstorev1.RegisterOperationServiceServer(server, &operationServiceServer{ops: ops})
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
	if exists {
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

	snapshots, err := storage.MarkDeleting(ctx, s.lvm, s.cfg, request.VolumeId, request.Force)
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

	err := storage.DeleteSnapshot(ctx, s.lvm, s.cfg, request.SnapshotId)
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

// volumeError maps storage errors onto grpc codes. Internal failures
// are logged and answered without detail.
func volumeError(ctx context.Context, err error, message string) error {
	var wrongState *storage.WrongStateError
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return status.Error(codes.NotFound, "volume does not exist")

	case errors.Is(err, storage.ErrShrink), errors.Is(err, storage.ErrHasSnapshots):
		return status.Error(codes.FailedPrecondition, err.Error())

	case errors.As(err, &wrongState):
		return status.Error(codes.FailedPrecondition, wrongState.Error())

	case errors.Is(err, lvm.ErrInUse):
		return status.Error(codes.FailedPrecondition, "volume is in use")

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

	case storage.StateIncoming:
		return beanstorev1.VolumeState_VOLUME_STATE_INCOMING

	case storage.StateRetired:
		return beanstorev1.VolumeState_VOLUME_STATE_RETIRED

	case storage.StateDeleting:
		return beanstorev1.VolumeState_VOLUME_STATE_DELETING

	case storage.StateSnapshot:
		return beanstorev1.VolumeState_VOLUME_STATE_SNAPSHOT

	default:
		return beanstorev1.VolumeState_VOLUME_STATE_UNSPECIFIED
	}
}
