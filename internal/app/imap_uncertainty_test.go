package app

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func uncertainIMAPServer(t *testing.T) (int, *atomic.Int32) { return draftIMAPServer(t, "missing-uid") }

func draftIMAPServer(t *testing.T, behavior string) (int, *atomic.Int32) {
	t.Helper()
	cert := httptest.NewTLSServer(nil)
	tc := cert.TLS.Clone()
	cert.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	count := new(atomic.Int32)
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				fmt.Fprint(c, "* OK synthetic\r\n")
				r := bufio.NewReader(c)
				for {
					line, e := r.ReadString('\n')
					if e != nil {
						return
					}
					fields := strings.Fields(line)
					if len(fields) < 2 {
						return
					}
					tag, cmd := fields[0], fields[1]
					switch cmd {
					case "STARTTLS":
						fmt.Fprintf(c, "%s OK tls\r\n", tag)
						c = tls.Server(c, tc)
						if c.(*tls.Conn).Handshake() != nil {
							return
						}
						r = bufio.NewReader(c)
					case "APPEND":
						if behavior == "reject" {
							fmt.Fprintf(c, "%s NO rejected before literal\r\n", tag)
							continue
						}
						n, e := strconv.Atoi(strings.Trim(fields[len(fields)-1], "{}"))
						if e != nil {
							return
						}
						fmt.Fprint(c, "+ ready\r\n")
						if _, e = io.CopyN(io.Discard, r, int64(n+2)); e != nil {
							return
						}
						count.Add(1)
						if behavior == "lost-completion" {
							return
						}
						fmt.Fprintf(c, "%s OK appended\r\n", tag)
					case "LOGOUT":
						fmt.Fprintf(c, "%s OK bye\r\n", tag)
						return
					default:
						fmt.Fprintf(c, "%s OK done\r\n", tag)
					}
				}
			}(c)
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, count
}
func imapCommandArgs(t *testing.T, port int) []string {
	t.Setenv("PMAIL_USE_LOCAL_STATE", "0")
	t.Setenv("PMAIL_SMTP_PASSWORD", "synthetic-only")
	root := t.TempDir()
	cfg := config.Default()
	cfg.Timeout = "2s"
	cfg.Bridge.Host = "127.0.0.1"
	cfg.Bridge.IMAPPort = port
	cfg.Bridge.Username = "synthetic@example.invalid"
	p := filepath.Join(root, "config.toml")
	if err := config.Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	return []string{"--json", "--config", p, "--state", filepath.Join(root, "state.json")}
}

func TestUncertainDraftDoesNotAdvertiseRetry(t *testing.T) {
	for _, behavior := range []string{"missing-uid", "lost-completion", "reject"} {
		t.Run(behavior, func(t *testing.T) {
			port, count := draftIMAPServer(t, behavior)
			args := append(imapCommandArgs(t, port), "draft", "create", "--to", "synthetic@example.invalid", "--body", "synthetic", "--idempotency-key", "audit-key")
			previous := smtpSendFn
			t.Cleanup(func() { smtpSendFn = previous })
			smtpCalls := 0
			smtpSendFn = func(bridge.SMTPConfig, bridge.SendInput) error {
				smtpCalls++
				return fmt.Errorf("synthetic fallback rejected")
			}
			var out bytes.Buffer
			exit := Run(args, strings.NewReader(""), &out, io.Discard)
			if behavior == "reject" {
				if exit != 4 || count.Load() != 0 || smtpCalls != 1 || !strings.Contains(out.String(), `"code":"imap_draft_create_failed"`) || !strings.Contains(out.String(), `"retryable":true`) {
					t.Fatalf("definite failure: exit=%d appends=%d smtp=%d output=%s", exit, count.Load(), smtpCalls, out.String())
				}
				return
			}
			if exit != 4 || count.Load() != 1 || smtpCalls != 0 {
				t.Fatalf("uncertain dispatch: exit=%d appends=%d smtp=%d output=%s", exit, count.Load(), smtpCalls, out.String())
			}
			for _, want := range []string{`"retryable":false`, `"code":"imap_draft_create_uncertain"`, `"category":"uncertain"`, `Inspect Drafts before retrying`} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("missing %q: %s", want, out.String())
				}
			}
		})
	}
}

func TestDraftCreationErrorDistinguishesDefiniteFailures(t *testing.T) {
	definite := draftCreationError(fmt.Errorf("synthetic pre-write failure"))
	if classified := classifyCLIError(definite.code, definite.exit); !classified.Retryable || classified.Category != "transient" {
		t.Fatalf("definite failure: %+v", classified)
	}
}
