package bridge

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestTLSCertificateFileAcceptsLargeRegularBundle(t *testing.T) {
	_, path := smtpTestCertificate(t, true)
	pem, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Existing CLI configurations can provide a large regular PEM bundle.
	contents := bytes.Repeat(pem, (1024*1024)/len(pem)+1)
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"IMAP", "SMTP"} {
		if _, err := bridgeTLSConfig(protocol, "127.0.0.1", path); err != nil {
			t.Fatalf("%s large regular certificate bundle: %v", protocol, err)
		}
	}
}

func TestTLSCertificateBundleChecksEveryCertificate(t *testing.T) {
	_, path := smtpTestCertificate(t, true)
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		"-----BEGIN CERTIFICATE-----\nAA",
		"-----BEGIN CERTIFICATE-----\n@@\n-----END CERTIFICATE-----\n",
		"-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n",
	} {
		for _, bundle := range []string{string(valid) + invalid, invalid + "\n" + string(valid)} {
			if err := os.WriteFile(path, []byte(bundle), 0600); err != nil {
				t.Fatal(err)
			}
			for _, protocol := range []string{"IMAP", "SMTP"} {
				if _, err := bridgeTLSConfig(protocol, "localhost", path); !errors.Is(err, ErrTLSConfig) {
					t.Errorf("%s accepted malformed entry in a mixed bundle: %v", protocol, err)
				}
			}
		}
	}
	commented := "# Trusted local certificate\n" + string(valid) + "\n# Second certificate\n" + string(valid)
	if err := os.WriteFile(path, []byte(commented), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := bridgeTLSConfig("IMAP", "localhost", path); err != nil {
		t.Fatalf("valid annotated bundle: %v", err)
	}
}
