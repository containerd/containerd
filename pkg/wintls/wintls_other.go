//go:build !windows
// +build !windows

package wintls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
)

// CertResource is a platform-agnostic interface for cleaning up TLS resources
type CertResource interface {
	Close() error
}

// NoopCertResource implements CertResource for non-Windows platforms
type NoopCertResource struct{}

func (n *NoopCertResource) Close() error {
	return nil
}

// Stub for non-Windows platforms
func SetupTLSFromWindowsCertStore(ctx context.Context, commonName string) (*tls.Config, *x509.CertPool, CertResource, error) {
	return nil, nil, &NoopCertResource{}, nil
}
