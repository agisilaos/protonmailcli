package bridge

import (
	"crypto/tls"
	"fmt"
)

// TLSPolicy holds the trust and hostname already validated for a Bridge
// operation. IMAP and SMTP use independent copies of its TLS configuration.
type TLSPolicy struct {
	config          *tls.Config
	certificateFile string
}

func (p *TLSPolicy) configFor(protocol, host, certificateFile string) (*tls.Config, error) {
	if p == nil {
		return bridgeTLSConfig(protocol, host, certificateFile)
	}
	if p.config == nil || p.config.ServerName != host || p.certificateFile != certificateFile {
		return nil, fmt.Errorf("%w: %s host or certificate setting differs from the prepared policy", ErrTLSConfig, protocol)
	}
	return p.config.Clone(), nil
}
