package bridge

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

var ErrTLSConfig = errors.New("invalid TLS configuration")

func bridgeTLSConfig(protocol, host, certificateFile string) (*tls.Config, error) {
	config := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if certificateFile == "" {
		return config, nil
	}
	certPEM, err := os.ReadFile(certificateFile)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s TLS certificate: %w", ErrTLSConfig, protocol, err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("%w: load system certificate roots: %w", ErrTLSConfig, err)
	}
	if !roots.AppendCertsFromPEM(certPEM) {
		return nil, fmt.Errorf("%w: %s TLS certificate file contains no PEM certificates", ErrTLSConfig, protocol)
	}
	config.RootCAs = roots
	return config, nil
}
