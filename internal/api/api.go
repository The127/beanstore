package api

import (
	"context"
	"regexp"

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
		})
	}

	return response, nil
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

	default:
		return beanstorev1.VolumeState_VOLUME_STATE_UNSPECIFIED
	}
}
