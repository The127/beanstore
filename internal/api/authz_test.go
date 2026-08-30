package api

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/internal/storage"
	"github.com/The127/beanstore/internal/testpki"
)

func TestRoleAuthorization(t *testing.T) {
	pki := testpki.New(t)
	fake := &fakeRunner{outputs: []string{noLVs}}
	volumes, _ := testServer(t, fake)

	options, err := ServerOptions(config.Config{
		VolumeGroup: "vg0", ThinPool: "pool0", TLS: pki.Leaf(t, "server"),
	})
	require.NoError(t, err)

	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(options...)
	beanstorev1.RegisterVolumeServiceServer(server, volumes)
	beanstorev1.RegisterTransferServiceServer(server, &transferServiceServer{
		transfers: storage.NewTransfers(t.Context(), volumes.lvm, volumes.cfg),
		lvm:       volumes.lvm, cfg: volumes.cfg,
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	dialer := grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	})
	dial := func(tls config.TLS) *grpc.ClientConn {
		creds, err := clientCredentials(config.Config{TLS: tls})
		require.NoError(t, err)
		conn, err := grpc.NewClient("passthrough:///localhost", dialer,
			grpc.WithTransportCredentials(creds))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })

		return conn
	}

	orchestrator := dial(pki.Leaf(t, "orchestrator", RoleOrchestrator))
	_, err = beanstorev1.NewVolumeServiceClient(orchestrator).ListVolumes(t.Context(),
		&beanstorev1.ListVolumesRequest{})
	require.NoError(t, err, "the orchestrator calls the control plane")

	node := dial(pki.Leaf(t, "node-2", RoleNode))
	_, err = beanstorev1.NewVolumeServiceClient(node).ListVolumes(t.Context(),
		&beanstorev1.ListVolumesRequest{})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "nodes cannot touch the control plane")

	_, err = beanstorev1.NewTransferServiceClient(node).QueryTransfer(t.Context(),
		&beanstorev1.QueryTransferRequest{TransferId: "tr-1"})
	assert.Equal(t, codes.NotFound, status.Code(err), "the transfer plane answered, not the interceptor")

	stream, err := beanstorev1.NewVolumeServiceClient(node).Export(t.Context(),
		&beanstorev1.ExportRequest{SnapshotId: "snap-1"})
	require.NoError(t, err)
	_, err = stream.Recv()
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "streams are intercepted too")

	stranger := dial(pki.Leaf(t, "stranger"))
	_, err = beanstorev1.NewTransferServiceClient(stranger).QueryTransfer(t.Context(),
		&beanstorev1.QueryTransferRequest{TransferId: "tr-1"})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "a roleless certificate calls nothing")
}
