package api

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/internal/logging"
	"github.com/The127/beanstore/internal/storage"
	"github.com/The127/beanstore/lvm"
)

type volumeServiceServer struct {
	beanstorev1.UnimplementedVolumeServiceServer
	lvm *lvm.Client
	cfg config.Config
}

type operationServiceServer struct {
	beanstorev1.UnimplementedOperationServiceServer
}

// Register wires all beanstore services onto the given grpc server.
func Register(server *grpc.Server, client *lvm.Client, cfg config.Config) {
	beanstorev1.RegisterVolumeServiceServer(server, &volumeServiceServer{lvm: client, cfg: cfg})
	beanstorev1.RegisterOperationServiceServer(server, &operationServiceServer{})
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
