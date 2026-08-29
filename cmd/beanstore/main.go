package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/mdlayher/sdnotify"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/The127/beanstore/internal/api"
	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/internal/logging"
	"github.com/The127/beanstore/internal/storage"
	"github.com/The127/beanstore/lvm"
)

const defaultConfigPath = "/etc/beanstore/config.yaml"

func main() {
	err := run()
	if err != nil {
		slog.Error("beanstore failed", "error", err)
		os.Exit(1)
	}
}

func loadConfig() (config.Config, error) {
	configPath := flag.String("config", defaultConfigPath, "path to the config file")
	flag.Parse()

	// the default path may be absent on dev machines, an explicitly
	// given path must exist
	required := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			required = true
		}
	})

	return config.Load(*configPath, required)
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	ctx = logging.WithLogger(ctx, logger)

	lvmClient := lvm.New()
	err = storage.Setup(ctx, lvmClient, cfg)
	if err != nil {
		return fmt.Errorf("preparing storage: %w", err)
	}

	// the notifier is nil outside systemd, all methods no-op on nil
	notifier, err := sdnotify.New()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("creating systemd notifier: %w", err)
	}
	defer func() {
		_ = notifier.Close()
	}()

	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.ListenAddress, err)
	}

	server := grpc.NewServer()
	api.Register(ctx, server, lvmClient, cfg)
	reflection.Register(server)

	go func() {
		<-ctx.Done()

		logging.FromContext(ctx).Info("shutting down")
		_ = notifier.Notify(sdnotify.Stopping)
		server.GracefulStop()
	}()

	logger.Info("beanstore listening", "address", cfg.ListenAddress)

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
