package testpki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/credentials"

	"github.com/The127/beanstore/internal/config"
)

// Authority is a throwaway ca whose leaves are valid for localhost
// and 127.0.0.1.
type Authority struct {
	dir    string
	caFile string
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
	serial int64
}

// New builds a fresh authority in a test temp dir.
func New(t *testing.T) *Authority {
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
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	caFile := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(caFile,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600))

	return &Authority{dir: dir, caFile: caFile, caCert: caCert, caKey: caKey, serial: 1}
}

// Leaf mints a certificate whose roles sit in the OU.
func (a *Authority) Leaf(t *testing.T, name string, roles ...string) config.TLS {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	a.serial++
	template := &x509.Certificate{
		SerialNumber: big.NewInt(a.serial),
		Subject:      pkix.Name{CommonName: name, OrganizationalUnit: roles},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.caCert, &key.PublicKey, a.caKey)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	certFile := filepath.Join(a.dir, name+".pem")
	require.NoError(t, os.WriteFile(certFile,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	keyFile := filepath.Join(a.dir, name+".key")
	require.NoError(t, os.WriteFile(keyFile,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))

	return config.TLS{CAFile: a.caFile, CertFile: certFile, KeyFile: keyFile}
}

// ClientCredentials loads dialing credentials from the leaf files.
func ClientCredentials(t *testing.T, material config.TLS) credentials.TransportCredentials {
	t.Helper()

	certificate, err := tls.LoadX509KeyPair(material.CertFile, material.KeyFile)
	require.NoError(t, err)
	ca, err := os.ReadFile(material.CAFile)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(ca))

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{certificate},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	})
}
