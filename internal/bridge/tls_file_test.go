package bridge

import (
	"bytes"
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
