package bridge

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

func bridgeTLSConfig(protocol, host, certificateFile string) (*tls.Config, error) {
	config := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if certificateFile == "" {
		return config, nil
	}
	certPEM, err := os.ReadFile(certificateFile)
	if err != nil {
		return nil, fmt.Errorf("read %s TLS certificate: %w", protocol, err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system certificate roots: %w", err)
	}
	if !roots.AppendCertsFromPEM(certPEM) {
		return nil, fmt.Errorf("%s TLS certificate file contains no PEM certificates", protocol)
	}
	config.RootCAs = roots
	return config, nil
}
