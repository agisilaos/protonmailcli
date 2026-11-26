package bridge

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
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
