package api

import (
	"google.golang.org/grpc"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
)

type volumeServiceServer struct {
	beanstorev1.UnimplementedVolumeServiceServer
}

type operationServiceServer struct {
	beanstorev1.UnimplementedOperationServiceServer
}

// Register wires all beanstore services onto the given grpc server.
func Register(server *grpc.Server) {
	beanstorev1.RegisterVolumeServiceServer(server, &volumeServiceServer{})
	beanstorev1.RegisterOperationServiceServer(server, &operationServiceServer{})
}
