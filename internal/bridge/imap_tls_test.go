package bridge

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIMAPTLSPolicyVerifiesEveryConfiguredHost(t *testing.T) {
	for _, host := range []string{"mail.example.invalid", "localhost", "127.0.0.1", "::1"} {
		t.Run(host, func(t *testing.T) {
			policy, err := (IMAPConfig{Host: host}).tlsConfig()
			if err != nil {
				t.Fatal(err)
			}
			if policy.InsecureSkipVerify || policy.ServerName != host || policy.MinVersion < tls.VersionTLS12 {
				t.Fatalf("IMAP TLS policy: skip verification=%t, host=%q, minimum version=%x", policy.InsecureSkipVerify, policy.ServerName, policy.MinVersion)
			}
			if policy.RootCAs != nil {
				t.Fatal("default policy must use system certificate roots")
			}
		})
	}
}

func TestDialIMAPRejectsInvalidServerCertificateBeforeLogin(t *testing.T) {
	for _, kind := range []string{"untrusted", "wrong hostname"} {
		t.Run(kind, func(t *testing.T) {
			serverTLS, certificateFile := smtpTestCertificate(t, kind == "untrusted")
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			type handshakeResult struct {
				commands string
				err      error
			}
			done := make(chan handshakeResult, 1)
			go func() {
				var result handshakeResult
				defer func() { done <- result }()
				conn, err := listener.Accept()
				if err != nil {
					result.err = err
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(2 * time.Second))
				if _, err := fmt.Fprint(conn, "* OK local TLS policy test\r\n"); err != nil {
					result.err = err
					return
				}
				result.commands, err = bufio.NewReader(conn).ReadString('\n')
				fields := strings.Fields(result.commands)
				if err != nil || len(fields) != 2 || fields[1] != "STARTTLS" {
					result.err = fmt.Errorf("expected STARTTLS, got %q: %v", result.commands, err)
					return
				}
				if _, err := fmt.Fprintf(conn, "%s OK begin TLS\r\n", fields[0]); err != nil {
					result.err = err
					return
				}
				tlsConn := tls.Server(conn, serverTLS)
				result.err = tlsConn.Handshake()
				if result.err == nil {
					line, _ := bufio.NewReader(tlsConn).ReadString('\n')
					result.commands += line
				}
			}()

			// No credentials are supplied: this test only exercises TLS rejection.
			cfg := IMAPConfig{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port}
			if kind == "wrong hostname" {
				cfg.TLSCertFile = certificateFile
			}
			client, dialErr := DialIMAP(cfg, time.Second)
			listener.Close()
			if client != nil {
				client.Close()
			}
			result := <-done
			var verificationErr *tls.CertificateVerificationError
			if !errors.As(dialErr, &verificationErr) {
				t.Fatalf("expected TLS certificate rejection, got %v", dialErr)
			}
			if kind == "untrusted" {
				var authorityErr x509.UnknownAuthorityError
				if !errors.As(dialErr, &authorityErr) {
					t.Fatalf("expected untrusted authority rejection, got %v", dialErr)
				}
			} else {
				var hostnameErr x509.HostnameError
				if !errors.As(dialErr, &hostnameErr) {
					t.Fatalf("expected hostname rejection, got %v", dialErr)
				}
			}
			if result.err == nil || strings.Contains(strings.ToUpper(result.commands), "LOGIN") || len(strings.Fields(result.commands)) != 2 {
				t.Fatalf("server must reject TLS after STARTTLS alone: commands=%q error=%v", result.commands, result.err)
			}
		})
	}
}

func TestIMAPTLSPolicyIncludesConfiguredCertificate(t *testing.T) {
	serverTLS, path := smtpTestCertificate(t, true)
	policy, err := (IMAPConfig{Host: "127.0.0.1", TLSCertFile: path}).tlsConfig()
	if err != nil {
		t.Fatal(err)
	}
	if policy.InsecureSkipVerify || policy.RootCAs == nil || policy.ServerName != "127.0.0.1" {
		t.Fatalf("explicit trust policy: skip verification=%t, host=%q, configured roots=%t", policy.InsecureSkipVerify, policy.ServerName, policy.RootCAs != nil)
	}
	certificate, err := x509.ParseCertificate(serverTLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: policy.RootCAs, DNSName: policy.ServerName}); err != nil {
		t.Fatalf("configured certificate is not trusted by IMAP policy: %v", err)
	}
}

func TestIMAPTLSPolicyRejectsInvalidCertificateFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(path, []byte("not a PEM certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + ".missing"} {
		if _, err := (IMAPConfig{Host: "mail.example.invalid", TLSCertFile: name}).tlsConfig(); err == nil {
			t.Fatalf("invalid trust file accepted: %s", name)
		}
	}
}
