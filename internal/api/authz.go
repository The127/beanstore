package api

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/The127/beanstore/internal/config"
)

// Certificate roles, read from the client certificate's OU.
const (
	RoleOrchestrator = "beanstore-orchestrator"
	RoleNode         = "beanstore-node"
)

// transferServicePrefix is the node-to-node plane, the only surface a
// node role may call.
const transferServicePrefix = "/beanstore.v1.TransferService/"

// ServerOptions builds the daemon's transport credentials and, with
// TLS, the role authorization interceptors.
func ServerOptions(cfg config.Config) ([]grpc.ServerOption, error) {
	creds, err := ServerCredentials(cfg)
	if err != nil {
		return nil, err
	}

	options := []grpc.ServerOption{grpc.Creds(creds)}
	if cfg.TLS.Enabled() {
		options = append(options,
			grpc.ChainUnaryInterceptor(authorizeUnary),
			grpc.ChainStreamInterceptor(authorizeStream),
		)
	}

	return options, nil
}

func authorizeUnary(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	err := authorize(ctx, info.FullMethod)
	if err != nil {
		return nil, err
	}

	return handler(ctx, request)
}

func authorizeStream(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	err := authorize(stream.Context(), info.FullMethod)
	if err != nil {
		return err
	}

	return handler(server, stream)
}

// authorize maps the peer certificate's role onto the method: the
// orchestrator role calls everything, the node role only the
// transfer plane.
func authorize(ctx context.Context, fullMethod string) error {
	role := peerRole(ctx)
	switch {
	case role == RoleOrchestrator:
		return nil

	case role == RoleNode && strings.HasPrefix(fullMethod, transferServicePrefix):
		return nil

	case role == RoleNode:
		return status.Error(codes.PermissionDenied, "the node role only calls the transfer plane")

	default:
		return status.Error(codes.PermissionDenied, "the client certificate carries no beanstore role")
	}
}

func peerRole(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.VerifiedChains) == 0 || len(info.State.VerifiedChains[0]) == 0 {
		return ""
	}

	for _, ou := range info.State.VerifiedChains[0][0].Subject.OrganizationalUnit {
		if ou == RoleOrchestrator || ou == RoleNode {
			return ou
		}
	}

	return ""
}
