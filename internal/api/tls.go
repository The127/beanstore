package api

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/The127/beanstore/internal/config"
)

// ServerCredentials builds the daemon's listening credentials: mutual
// TLS against the configured ca, or plaintext with the insecure opt
// in.
func ServerCredentials(cfg config.Config) (credentials.TransportCredentials, error) {
	if !cfg.TLS.Enabled() {
		return insecure.NewCredentials(), nil
	}

	certificate, pool, err := loadTLS(cfg.TLS)
	if err != nil {
		return nil, err
	}

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

// clientCredentials builds the dialing credentials for push targets.
func clientCredentials(cfg config.Config) (credentials.TransportCredentials, error) {
	if !cfg.TLS.Enabled() {
		return insecure.NewCredentials(), nil
	}

	certificate, pool, err := loadTLS(cfg.TLS)
	if err != nil {
		return nil, err
	}

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{certificate},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

func loadTLS(cfg config.TLS) (tls.Certificate, *x509.CertPool, error) {
	certificate, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("loading the tls key pair: %w", err)
	}

	ca, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("reading the tls ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return tls.Certificate{}, nil, fmt.Errorf("no certificates in %s", cfg.CAFile)
	}

	return certificate, pool, nil
}
