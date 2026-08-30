package api

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/internal/testpki"
)

func testPKI(t *testing.T) config.TLS {
	t.Helper()

	return testpki.New(t).Leaf(t, "leaf")
}

func TestMutualTLSHandshake(t *testing.T) {
	cfg := config.Config{VolumeGroup: "vg0", ThinPool: "pool0", TLS: testPKI(t)}

	serverCreds, err := ServerCredentials(cfg)
	require.NoError(t, err)
	clientCreds, err := clientCredentials(cfg)
	require.NoError(t, err)

	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.Creds(serverCreds))
	volumes, _ := testServer(t, &fakeRunner{outputs: []string{noLVs}})
	beanstorev1.RegisterVolumeServiceServer(server, volumes)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	dialer := grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	})

	conn, err := grpc.NewClient("passthrough:///localhost", dialer,
		grpc.WithTransportCredentials(clientCreds))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = beanstorev1.NewVolumeServiceClient(conn).ListVolumes(t.Context(),
		&beanstorev1.ListVolumesRequest{})
	require.NoError(t, err, "the mutual handshake succeeds")

	// a client without a certificate is refused
	bare, err := grpc.NewClient("passthrough:///localhost", dialer,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = bare.Close() })

	_, err = beanstorev1.NewVolumeServiceClient(bare).ListVolumes(t.Context(),
		&beanstorev1.ListVolumesRequest{})
	assert.Equal(t, codes.Unavailable, status.Code(err), "plaintext clients cannot connect")
}

func TestServerCredentialsInsecureFallback(t *testing.T) {
	creds, err := ServerCredentials(config.Config{})

	require.NoError(t, err)
	assert.Equal(t, "insecure", creds.Info().SecurityProtocol)
}

func TestServerCredentialsRejectsBrokenMaterial(t *testing.T) {
	pki := testPKI(t)
	pki.KeyFile = pki.CAFile

	_, err := ServerCredentials(config.Config{TLS: pki})

	assert.ErrorContains(t, err, "key pair")
}
