package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/config"
)

// testPKI writes a throwaway ca plus one leaf usable as server and
// client for the dns name "localhost".
func testPKI(t *testing.T) config.TLS {
	t.Helper()
	dir := t.TempDir()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "beanstore-test-ca"},
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "node-1"},
		DNSNames:     []string{"localhost"},
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	require.NoError(t, err)

	write := func(name, blockType string, der []byte) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path,
			pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600))

		return path
	}

	return config.TLS{
		CAFile:   write("ca.pem", "CERTIFICATE", caDER),
		CertFile: write("leaf.pem", "CERTIFICATE", leafDER),
		KeyFile:  write("leaf.key", "EC PRIVATE KEY", leafKeyDER),
	}
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
