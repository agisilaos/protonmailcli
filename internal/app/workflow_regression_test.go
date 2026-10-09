package app

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
)

// workflowServer is a loopback-only IMAP peer. It never contacts a mail provider.
type workflowServer struct {
	mu            sync.Mutex
	commands      []string
	appended      []string
	draftBox      string
	boxes         []string
	fetchFailure  bool
	appendFailure bool
	deleteFailure bool
	missing       bool
	raw           string
}

func (s *workflowServer) snapshot() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...), append([]string(nil), s.appended...)
}
func newWorkflowServer(t *testing.T, s *workflowServer) int {
	t.Helper()
	cert := httptest.NewTLSServer(nil)
	tc := cert.TLS.Clone()
	cert.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	if s.draftBox == "" {
		s.draftBox = "Drafts"
	}
	if s.raw == "" {
		s.raw = "From: sender@example.invalid\r\nTo: recipient@example.invalid\r\nSubject: original\r\nMessage-ID: <draft@example.invalid>\r\nIn-Reply-To: <parent@example.invalid>\r\nReferences: <root@example.invalid> <parent@example.invalid>\r\n\r\noriginal body"
	}
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
					s.mu.Lock()
					s.commands = append(s.commands, strings.TrimSpace(line))
					s.mu.Unlock()
					switch cmd {
					case "STARTTLS":
						fmt.Fprintf(c, "%s OK tls\r\n", tag)
						c = tls.Server(c, tc)
						if c.(*tls.Conn).Handshake() != nil {
							return
						}
						r = bufio.NewReader(c)
					case "CAPABILITY":
						fmt.Fprintf(c, "* CAPABILITY IMAP4rev1 UIDPLUS MOVE\r\n%s OK capabilities\r\n", tag)
					case "LIST":
						fmt.Fprintf(c, "* LIST (\\Drafts) \"/\" %q\r\n", s.draftBox)
						for _, b := range s.boxes {
							fmt.Fprintf(c, "* LIST () \"/\" %q\r\n", b)
						}
						fmt.Fprintf(c, "%s OK listed\r\n", tag)
					case "UID":
						switch fields[2] {
						case "SEARCH":
							fmt.Fprintf(c, "* SEARCH 41\r\n%s OK searched\r\n", tag)
						case "FETCH":
							if s.fetchFailure {
								fmt.Fprintf(c, "%s NO synthetic fetch failure\r\n", tag)
							} else if s.missing || fields[3] == "404" {
								fmt.Fprintf(c, "%s OK absent\r\n", tag)
							} else {
								s.mu.Lock()
								raw := s.raw
								s.mu.Unlock()
								fmt.Fprintf(c, "* 1 FETCH (UID %s FLAGS () BODY[] {%d}\r\n%s\r\n)\r\n%s OK fetched\r\n", fields[3], len(raw), raw, tag)
							}
						default:
							if s.deleteFailure && fields[2] == "STORE" {
								fmt.Fprintf(c, "%s NO deletion rejected\r\n", tag)
							} else {
								fmt.Fprintf(c, "%s OK done\r\n", tag)
							}
						}
					case "APPEND":
						if s.appendFailure {
							fmt.Fprintf(c, "%s NO append rejected\r\n", tag)
							continue
						}
						n, e := strconv.Atoi(strings.Trim(fields[len(fields)-1], "{}"))
						if e != nil {
							return
						}
						fmt.Fprint(c, "+ ready\r\n")
						data := make([]byte, n+2)
						if _, e := io.ReadFull(r, data); e != nil {
							return
						}
						s.mu.Lock()
						s.appended = append(s.appended, string(data[:n]))
						uid := 90 + len(s.appended)
						s.mu.Unlock()
						fmt.Fprintf(c, "%s OK [APPENDUID 123 %d] appended\r\n", tag, uid)
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
	return l.Addr().(*net.TCPAddr).Port
}
func runWorkflow(t *testing.T, args []string, input string) (int, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := Run(args, strings.NewReader(input), &out, &stderr)
	return code, out.String()
}
func captureWorkflowSMTP(t *testing.T, args []string) func() []string {
	return captureWorkflowSMTPWithCompletion(t, args, true)
}

func captureWorkflowSMTPWithCompletion(t *testing.T, args []string, acknowledge bool) func() []string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	var mu sync.Mutex
	var messages []string
	cfg, err := config.Load(args[2])
	if err != nil {
		t.Fatal(err)
	}
	cfg.Bridge.SMTPPort = l.Addr().(*net.TCPAddr).Port
	if err := config.Save(args[2], cfg); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				fmt.Fprint(c, "220 synthetic\r\n")
				r := bufio.NewReader(c)
				for {
					line, e := r.ReadString('\n')
					if e != nil {
						return
					}
					switch strings.Fields(line)[0] {
					case "EHLO":
						fmt.Fprint(c, "250-synthetic\r\n250 AUTH PLAIN\r\n")
					case "AUTH":
						fmt.Fprint(c, "235 authenticated\r\n")
					case "DATA":
						fmt.Fprint(c, "354 ready\r\n")
						var b strings.Builder
						for {
							line, e = r.ReadString('\n')
							if e != nil {
								return
							}
							if line == ".\r\n" {
								break
							}
							b.WriteString(line)
						}
						mu.Lock()
						messages = append(messages, b.String())
						mu.Unlock()
						if !acknowledge {
							return
						}
						fmt.Fprint(c, "250 accepted\r\n")
					case "QUIT":
						fmt.Fprint(c, "221 bye\r\n")
						return
					default:
						fmt.Fprint(c, "250 OK\r\n")
					}
				}
			}(c)
		}
	}()
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), messages...) }
}
func TestPB15IMAPSendUsesConfiguredTimeout(t *testing.T) {
	s := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, e := l.Accept()
		if e == nil {
			defer c.Close()
			time.Sleep(time.Second)
		}
	}()
	cfg, err := config.Load(args[2])
	if err != nil {
		t.Fatal(err)
	}
	cfg.Timeout = "100ms"
	cfg.Bridge.SMTPPort = l.Addr().(*net.TCPAddr).Port
	if err := config.Save(args[2], cfg); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	code, out := runWorkflow(t, append(args, "message", "send", "--draft-id", "41", "--confirm-send", "41"), "")
	if code != 4 || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("exit=%d elapsed=%v out=%s", code, time.Since(start), out)
	}
}

func TestPB14IMAPBatchPassesExplicitTLSTrust(t *testing.T) {
	s := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	cfg, err := config.Load(args[2])
	if err != nil {
		t.Fatal(err)
	}
	cfg.Bridge.TLSCertFile = "~/synthetic-bridge.pem"
	if err := config.Save(args[2], cfg); err != nil {
		t.Fatal(err)
	}
	old := smtpSendFn
	t.Cleanup(func() { smtpSendFn = old })
	smtpSendFn = func(got bridge.SMTPConfig, _ bridge.SendInput) error {
		if got.TLSCertFile != config.Expand(cfg.Bridge.TLSCertFile) {
			return fmt.Errorf("missing explicit TLS trust path: %q", got.TLSCertFile)
		}
		return nil
	}
	code, out := runWorkflow(t, append(args, "message", "send-many", "--stdin"), `[{"draft_id":"41","confirm_send":"41"}]`)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
}
