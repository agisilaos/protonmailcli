package app

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"protonmailcli/internal/config"
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
