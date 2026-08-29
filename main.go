package main

import (
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
)

// hardcoded until a config mechanism exists
const listenAddress = "127.0.0.1:50051"

type volumeServiceServer struct {
	beanstorev1.UnimplementedVolumeServiceServer
}

type operationServiceServer struct {
	beanstorev1.UnimplementedOperationServiceServer
}

func main() {
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		log.Fatalf("listening on %s: %v", listenAddress, err)
	}

	server := grpc.NewServer()
	beanstorev1.RegisterVolumeServiceServer(server, &volumeServiceServer{})
	beanstorev1.RegisterOperationServiceServer(server, &operationServiceServer{})
	reflection.Register(server)

	log.Printf("beanstore listening on %s", listenAddress)

	err = server.Serve(listener)
	if err != nil {
		log.Fatalf("serving grpc: %v", err)
	}
}
