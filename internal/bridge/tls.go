package bridge

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
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
	if err := addBridgeCertificates(roots, certPEM); err != nil {
		return nil, fmt.Errorf("%w: %s TLS certificate file: %w", ErrTLSConfig, protocol, err)
	}
	config.RootCAs = roots
	return config, nil
}

// Decode one delimited certificate at a time. pem.Decode on a whole bundle
// can skip an invalid entry and silently return the next valid certificate.
func addBridgeCertificates(roots *x509.CertPool, data []byte) error {
	start, offset := -1, 0
	found := false
	for line := range bytes.Lines(data) {
		offset += len(line)
		switch string(bytes.TrimSpace(line)) {
		case "-----BEGIN CERTIFICATE-----":
			if start >= 0 {
				return errors.New("certificate is missing its end marker")
			}
			start = offset - len(line)
		case "-----END CERTIFICATE-----":
			if start < 0 {
				return errors.New("certificate is missing its begin marker")
			}
			block, _ := pem.Decode(data[start:offset])
			if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
				return errors.New("invalid certificate PEM block")
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return fmt.Errorf("invalid certificate: %w", err)
			}
			roots.AddCert(cert)
			start, found = -1, true
		}
	}
	if start >= 0 {
		return errors.New("certificate is missing its end marker")
	}
	if !found {
		return errors.New("contains no PEM certificates")
	}
	return nil
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
