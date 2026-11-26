package app

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"protonmailcli/internal/config"
	"protonmailcli/internal/output"
)

func TestLocalTLSConfigFailureIsNonretryableBeforeDial(t *testing.T) {
	for _, kind := range []string{"missing", "invalid PEM"} {
		t.Run(kind, func(t *testing.T) {
			c := newRegressionCLI(t)
			t.Setenv("PMAIL_USE_LOCAL_STATE", "0")
			t.Setenv("PMAIL_SMTP_PASSWORD", "synthetic-only")
			listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			cfg, err := config.Load(c.config)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Bridge.Host = "127.0.0.1"
			cfg.Bridge.IMAPPort = listener.Addr().(*net.TCPAddr).Port
			cfg.Bridge.TLSCertFile = filepath.Join(t.TempDir(), "trust.pem")
			if kind == "invalid PEM" {
				if err := os.WriteFile(cfg.Bridge.TLSCertFile, []byte("not a certificate"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := config.Save(c.config, cfg); err != nil {
				t.Fatal(err)
			}
			code, out := c.run("", "--dry-run", "message", "send", "--draft-id", "imap:Drafts:41", "--confirm-send", "imap:Drafts:41")
			if code != 3 || !strings.Contains(out, `"code":"config_error"`) || !strings.Contains(out, `"retryable":false`) {
				t.Fatalf("local certificate error must not invite network retries: exit=%d output=%s", code, out)
			}
			listener.SetDeadline(time.Now().Add(20 * time.Millisecond))
			if conn, err := listener.Accept(); err == nil {
				conn.Close()
				t.Fatal("dialed IMAP despite invalid local certificate configuration")
			} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
				t.Fatal(err)
			}
		})
	}
}

func TestIMAPTLSFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name       string
		trust      bool
		validHost  bool
		disconnect bool
		wantExit   int
		wantCode   string
		retryable  bool
	}{
		{name: "untrusted issuer", validHost: true, wantExit: 3, wantCode: "config_error"},
		{name: "hostname mismatch", trust: true, wantExit: 3, wantCode: "config_error"},
		{name: "interrupted handshake", disconnect: true, wantExit: 4, wantCode: "imap_connect_failed", retryable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newRegressionCLI(t)
			t.Setenv("PMAIL_USE_LOCAL_STATE", "0")
			t.Setenv("PMAIL_SMTP_PASSWORD", "synthetic-only")
			port, certFile := tlsFailureEndpoint(t, tc.validHost, tc.disconnect)
			cfg, err := config.Load(c.config)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Bridge.Host = "127.0.0.1"
			cfg.Bridge.IMAPPort = port
			cfg.Timeout = "1s"
			if tc.trust {
				cfg.Bridge.TLSCertFile = certFile
			}
			if err := config.Save(c.config, cfg); err != nil {
				t.Fatal(err)
			}
			code, out := c.run("", "--dry-run", "message", "send", "--draft-id", "imap:Drafts:41", "--confirm-send", "imap:Drafts:41")
			var envelope output.Envelope
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatal(err)
			}
			if code != tc.wantExit || envelope.Error == nil || envelope.Error.Code != tc.wantCode || envelope.Error.Retryable != tc.retryable {
				t.Fatalf("TLS error classification: exit=%d output=%s", code, out)
			}
			if !tc.retryable && (!strings.Contains(envelope.Error.Hint, "bridge.host") || !strings.Contains(envelope.Error.Hint, "bridge.tls_cert_file")) {
				t.Fatalf("certificate correction hint must identify the hostname and trust settings: %s", out)
			}
		})
	}
}

func tlsFailureEndpoint(t *testing.T, validHostname, disconnect bool) (int, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic Bridge"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"wrong.example.invalid"},
	}
	if validHostname {
		certificate.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(t.TempDir(), "generated-trust.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		var result error
		defer func() { done <- result }()
		conn, err := listener.Accept()
		if err != nil {
			result = err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, result = fmt.Fprint(conn, "* OK local TLS error fixture\r\n"); result != nil {
			return
		}
		line, err := bufio.NewReader(conn).ReadString('\n')
		fields := strings.Fields(line)
		if err != nil || len(fields) != 2 || fields[1] != "STARTTLS" {
			result = fmt.Errorf("expected STARTTLS, got %q: %v", line, err)
			return
		}
		if _, result = fmt.Fprintf(conn, "%s OK begin TLS\r\n", fields[0]); result != nil {
			return
		}
		if disconnect {
			return
		}
		tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
		if tlsConn.Handshake() == nil {
			result = fmt.Errorf("expected client to reject fixture certificate")
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return listener.Addr().(*net.TCPAddr).Port, certFile
}
