package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/mdlayher/sdnotify"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/The127/beanstore/internal/api"
)

// hardcoded until a config mechanism exists
const listenAddress = "127.0.0.1:50051"

func main() {
	err := run()
	if err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// the notifier is nil outside systemd, all methods no-op on nil
	notifier, err := sdnotify.New()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("creating systemd notifier: %w", err)
	}
	defer func() {
		_ = notifier.Close()
	}()

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", listenAddress, err)
	}

	server := grpc.NewServer()
	api.Register(server)
	reflection.Register(server)

	go func() {
		<-ctx.Done()

		log.Print("shutting down")
		_ = notifier.Notify(sdnotify.Stopping)
		server.GracefulStop()
	}()

	log.Printf("beanstore listening on %s", listenAddress)

	err = notifier.Notify(sdnotify.Ready)
	if err != nil {
		return fmt.Errorf("notifying systemd: %w", err)
	}

	err = server.Serve(listener)
	if err != nil {
		return fmt.Errorf("serving grpc: %w", err)
	}

	return nil
}
