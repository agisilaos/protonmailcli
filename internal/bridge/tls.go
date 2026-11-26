package bridge

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

var ErrTLSConfig = errors.New("invalid TLS configuration")

func bridgeTLSConfig(protocol, host, certificateFile string) (*tls.Config, error) {
	config := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if certificateFile == "" {
		return config, nil
	}
	certPEM, err := readCertificateFile(certificateFile)
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

func readCertificateFile(path string) ([]byte, error) {
	// Open nonblocking before inspecting the descriptor so a FIFO, including a
	// path replaced during open, cannot stall certificate configuration.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("TLS certificate must be a regular file")
	}
	return io.ReadAll(f)
}
