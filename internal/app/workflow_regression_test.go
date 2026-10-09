package app

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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
func TestPB03TagDryRunDoesNotStore(t *testing.T) {
	for _, action := range []string{"add", "remove"} {
		t.Run(action, func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			args = append(args, "--dry-run", "tag", action, "--message-id", "41", "--tag", "reviewed")
			code, out := runWorkflow(t, args, "")
			cmds, _ := s.snapshot()
			if code != 0 || strings.Contains(strings.Join(cmds, "\n"), "UID STORE") || strings.Contains(out, `"changed":true`) {
				t.Fatalf("exit=%d out=%s commands=%v", code, out, cmds)
			}
		})
	}
}

func TestPB07TagPreservesMailbox(t *testing.T) {
	s := &workflowServer{}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "tag", "add", "--message-id", "imap:Archive:41", "--tag", "reviewed")
	code, out := runWorkflow(t, args, "")
	cmds, _ := s.snapshot()
	if code != 0 || !strings.Contains(strings.Join(cmds, "\n"), `SELECT "Archive"`) || !strings.Contains(out, `"messageId":"imap:Archive:41"`) {
		t.Fatalf("exit=%d out=%s commands=%v", code, out, cmds)
	}
}

func TestPB20ColonMailboxIDRoundTrip(t *testing.T) {
	s := &workflowServer{}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "message", "get", "--message-id", "imap:Project:2026:41")
	code, out := runWorkflow(t, args, "")
	cmds, _ := s.snapshot()
	if code != 0 || !strings.Contains(strings.Join(cmds, "\n"), `SELECT "Project:2026"`) || !strings.Contains(out, `"id":"imap:Project:2026:41"`) {
		t.Fatalf("exit=%d out=%s commands=%v", code, out, cmds)
	}
}

func TestPB21CollidingMailboxIDsAreAmbiguous(t *testing.T) {
	s := &workflowServer{boxes: []string{"Work-A", "Work_A"}}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "mailbox", "resolve", "--name", "work_a")
	code, out := runWorkflow(t, args, "")
	if code == 0 || !strings.Contains(out, "ambiguous") {
		t.Fatalf("exit=%d out=%s", code, out)
	}
}

func TestPB22DraftListingUsesDiscoveredMailbox(t *testing.T) {
	for _, cmd := range [][]string{{"draft", "list"}, {"search", "drafts"}} {
		t.Run(strings.Join(cmd, "-"), func(t *testing.T) {
			s := &workflowServer{draftBox: "Brouillons"}
			args := append(imapCommandArgs(t, newWorkflowServer(t, s)), cmd...)
			code, out := runWorkflow(t, args, "")
			cmds, _ := s.snapshot()
			if code != 0 || !strings.Contains(strings.Join(cmds, "\n"), `SELECT "Brouillons"`) {
				t.Fatalf("exit=%d out=%s commands=%v", code, out, cmds)
			}
		})
	}
}

func TestPB23SinceIDLowerBound(t *testing.T) {
	for _, kind := range []string{"messages", "drafts"} {
		t.Run(kind, func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			for _, tc := range []struct{ bound, count string }{{"42", "0"}, {"41", "1"}} {
				code, out := runWorkflow(t, append(append([]string(nil), args...), "search", kind, "--since-id", tc.bound), "")
				if code != 0 || !strings.Contains(out, `"count":`+tc.count) {
					t.Fatalf("bound=%s exit=%d out=%s", tc.bound, code, out)
				}
			}
		})
	}
}

func TestPB24DraftFetchFailureIsTransient(t *testing.T) {
	for _, cmd := range [][]string{{"draft", "get"}, {"draft", "update"}, {"message", "send"}} {
		t.Run(strings.Join(cmd, "-"), func(t *testing.T) {
			s := &workflowServer{fetchFailure: true}
			args := append(imapCommandArgs(t, newWorkflowServer(t, s)), cmd...)
			args = append(args, "--draft-id", "41")
			code, out := runWorkflow(t, args, "")
			if code != 4 || strings.Contains(out, `"code":"not_found"`) || !strings.Contains(out, `"retryable":true`) {
				t.Fatalf("exit=%d out=%s", code, out)
			}
		})
	}
}

func TestPB08FallbackPreservesIntendedRecipients(t *testing.T) {
	s := &workflowServer{appendFailure: true}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "create", "--to", "intended@example.invalid", "--body", "hello")
	oldSend, oldOpen := smtpSendFn, openBridgeClientFn
	t.Cleanup(func() { smtpSendFn = oldSend; openBridgeClientFn = oldOpen })
	var sent bridge.SendInput
	smtpSendFn = func(_ bridge.SMTPConfig, in bridge.SendInput) error { sent = in; return nil }
	openBridgeClientFn = func(config.Config, *model.State, string) (imapDraftClient, string, string, error) {
		return &fakeIMAPDraftClient{draftMailbox: "Drafts", searchUIDs: map[string][]string{"INBOX": {"88"}, "Drafts": {"99"}}}, "", "", nil
	}
	code, out := runWorkflow(t, args, "")
	if code != 0 || len(sent.To) != 1 || sent.To[0] != "intended@example.invalid" || len(sent.EnvelopeTo) != 1 || sent.EnvelopeTo[0] != "synthetic@example.invalid" {
		t.Fatalf("exit=%d To=%v out=%s", code, sent.To, out)
	}
}

func TestPB09FallbackNeverReturnsSourceUID(t *testing.T) {
	s := &workflowServer{appendFailure: true}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "create", "--to", "intended@example.invalid", "--body", "hello", "--idempotency-key", "fallback-identity")
	oldSend, oldOpen := smtpSendFn, openBridgeClientFn
	t.Cleanup(func() { smtpSendFn = oldSend; openBridgeClientFn = oldOpen })
	sends := 0
	smtpSendFn = func(bridge.SMTPConfig, bridge.SendInput) error { sends++; return nil }
	openBridgeClientFn = func(config.Config, *model.State, string) (imapDraftClient, string, string, error) {
		return &fakeIMAPDraftClient{draftMailbox: "Drafts", searchUIDs: map[string][]string{"INBOX": {"88"}, "Drafts": {}}}, "", "", nil
	}
	for i := 0; i < 2; i++ {
		code, out := runWorkflow(t, args, "")
		if code != 4 || !strings.Contains(out, `"code":"imap_draft_create_uncertain"`) || strings.Contains(out, "imap:Drafts:88") || sends != 1 {
			t.Fatalf("run=%d exit=%d sends=%d out=%s", i, code, sends, out)
		}
	}
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
func TestPB10ThreadHeadersSurviveSendAndUpdate(t *testing.T) {
	for _, action := range []string{"send", "send-many", "update"} {
		t.Run(action, func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			sent := captureWorkflowSMTP(t, args)
			input := ""
			if action == "update" {
				args = append(args, "draft", "update", "--draft-id", "41", "--subject", "changed")
			} else if action == "send" {
				args = append(args, "message", "send", "--draft-id", "41", "--confirm-send", "41")
			} else {
				args = append(args, "message", "send-many", "--stdin")
				input = `[{"draft_id":"41","confirm_send":"41"}]`
			}
			code, out := runWorkflow(t, args, input)
			_, raw := s.snapshot()
			if action != "update" {
				raw = sent()
			}
			if code != 0 || len(raw) != 1 || !strings.Contains(raw[0], "In-Reply-To: <parent@example.invalid>") || !strings.Contains(raw[0], "References: <root@example.invalid> <parent@example.invalid>") {
				t.Fatalf("exit=%d out=%s raw=%v", code, out, raw)
			}
		})
	}
}

func TestPB05DraftUpdatePreservesOriginalUntilReplacementConfirmed(t *testing.T) {
	for _, failure := range []bool{true, false} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			s := &workflowServer{appendFailure: failure}
			args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "update", "--draft-id", "41", "--subject", "changed")
			code, out := runWorkflow(t, args, "")
			cmds, _ := s.snapshot()
			appendAt, deleteAt := -1, -1
			for i, cmd := range cmds {
				if strings.Contains(cmd, " APPEND ") {
					appendAt = i
				}
				if strings.Contains(cmd, "UID STORE 41 ") {
					deleteAt = i
				}
			}
			if failure {
				if code == 0 || deleteAt >= 0 {
					t.Fatalf("failed replacement deleted original: exit=%d out=%s commands=%v", code, out, cmds)
				}
			} else if code != 0 || appendAt < 0 || deleteAt <= appendAt {
				t.Fatalf("replacement not preserved: exit=%d out=%s commands=%v", code, out, cmds)
			}
		})
	}
}

func TestPB27IMAPUpdateCanClearSubjectAndBody(t *testing.T) {
	s := &workflowServer{}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "update", "--draft-id", "41", "--subject", "", "--body", "")
	code, out := runWorkflow(t, args, "")
	_, raw := s.snapshot()
	if code != 0 || len(raw) != 1 || strings.Contains(raw[0], "Subject: original") || strings.Contains(raw[0], "original body") {
		t.Fatalf("exit=%d out=%s raw=%v", code, out, raw)
	}
}

func TestPB16IMAPRejectsInjectedHeadersBeforeWriting(t *testing.T) {
	for _, cmd := range [][]string{{"draft", "create", "--to", "recipient@example.invalid"}, {"draft", "update", "--draft-id", "41"}, {"message", "follow-up", "--message-id", "41"}} {
		t.Run(strings.Join(cmd[:2], "-"), func(t *testing.T) {
			s := &workflowServer{}
			args := append(imapCommandArgs(t, newWorkflowServer(t, s)), cmd...)
			args = append(args, "--subject", "subject\r\nBcc: injected@example.invalid\r\n\r\nchanged", "--body", "body")
			code, out := runWorkflow(t, args, "")
			cmds, raw := s.snapshot()
			if code != 2 || len(raw) != 0 || strings.Contains(strings.Join(cmds, "\n"), "UID STORE") {
				t.Fatalf("exit=%d out=%s raw=%v commands=%v", code, out, raw, cmds)
			}
		})
	}
}

func TestPB11DraftBatchHonorsItemKeys(t *testing.T) {
	for _, behavior := range []string{"confirmed", "missing-uid"} {
		t.Run(behavior, func(t *testing.T) {
			port, count := draftIMAPServer(t, behavior)
			args := append(imapCommandArgs(t, port), "draft", "create-many", "--stdin")
			input := `[{"to":["synthetic@example.invalid"],"body":"one","idempotency_key":"item-key"},{"to":["synthetic@example.invalid"],"body":"one","idempotency_key":"item-key"}]`
			wantExit := 0
			if behavior != "confirmed" {
				wantExit = 4
			}
			for i := 0; i < 2; i++ {
				code, out := runWorkflow(t, args, input)
				if code != wantExit || count.Load() != 1 {
					t.Fatalf("run=%d exit=%d appends=%d out=%s", i, code, count.Load(), out)
				}
			}
		})
	}
}

func TestPB11SendBatchHonorsItemKeys(t *testing.T) {
	s := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	sent := captureWorkflowSMTP(t, args)
	args = append(args, "message", "send-many", "--stdin")
	input := `[{"draft_id":"41","confirm_send":"41","idempotency_key":"send-item-key"},{"draft_id":"41","confirm_send":"41","idempotency_key":"send-item-key"}]`
	for i := 0; i < 2; i++ {
		code, out := runWorkflow(t, args, input)
		if code != 0 || len(sent()) != 1 {
			t.Fatalf("run=%d exit=%d sends=%d out=%s", i, code, len(sent()), out)
		}
	}
}

func TestPB12SendBatchReplayPreservesFailureExit(t *testing.T) {
	for _, input := range []string{`[{"draft_id":"41","confirm_send":"41"},{"draft_id":"404","confirm_send":"404"}]`, `[{"draft_id":"404","confirm_send":"404"}]`} {
		t.Run(input, func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			sent := captureWorkflowSMTP(t, args)
			args = append(args, "message", "send-many", "--stdin", "--idempotency-key", "batch-key")
			code, first := runWorkflow(t, args, input)
			next, second := runWorkflow(t, args, input)
			if code == 0 || next != code || len(sent()) > 1 {
				t.Fatalf("first=%d %s replay=%d %s sends=%d", code, first, next, second, len(sent()))
			}
		})
	}
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

func TestPB04SearchEscapesLiteralBackslashesAndRejectsControls(t *testing.T) {
	s := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	code, out := runWorkflow(t, append(append([]string(nil), args...), "search", "messages", "--subject", `C:\work "review"`), "")
	cmds, _ := s.snapshot()
	if code != 0 || !strings.Contains(strings.Join(cmds, "\n"), `SUBJECT "C:\\work \"review\""`) {
		t.Fatalf("literal exit=%d out=%s commands=%v", code, out, cmds)
	}
	code, out = runWorkflow(t, append(append([]string(nil), args...), "search", "messages", "--subject", "line\r\nnext"), "")
	if code != 2 {
		t.Fatalf("control exit=%d out=%s", code, out)
	}
}

func TestPB04TagSearchUsesIMAPKeywordAtom(t *testing.T) {
	s := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	code, out := runWorkflow(t, append(append([]string(nil), args...), "search", "messages", "--has-tag", "reviewed"), "")
	cmds, _ := s.snapshot()
	if code != 0 || !strings.Contains(strings.Join(cmds, "\n"), "UID SEARCH KEYWORD reviewed") {
		t.Fatalf("exit=%d out=%s commands=%v", code, out, cmds)
	}
	code, out = runWorkflow(t, append(append([]string(nil), args...), "search", "messages", "--has-tag", "bad tag"), "")
	if code != 2 {
		t.Fatalf("invalid keyword exit=%d out=%s", code, out)
	}
}

func TestDraftUpdatePartialOutcomePreservesConfirmedID(t *testing.T) {
	s := &workflowServer{deleteFailure: true}
	args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "draft", "update", "--draft-id", "41", "--subject", "changed")
	code, out := runWorkflow(t, args, "")
	_, raw := s.snapshot()
	if code != 4 || len(raw) != 1 || !strings.Contains(out, `"code":"imap_draft_update_uncertain"`) || !strings.Contains(out, `"retryable":false`) || !strings.Contains(out, "imap:Drafts:91") || !strings.Contains(out, "imap:Drafts:41") {
		t.Fatalf("exit=%d out=%s raw=%v", code, out, raw)
	}
}
func TestSendBatchStopsWhenAcceptedReceiptCannotBeSaved(t *testing.T) {
	s := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	sent := captureWorkflowSMTP(t, args)
	args = append(args, "message", "send-many", "--stdin")
	var out bytes.Buffer
	saves := 0
	a := App{Stdout: &out, Stderr: io.Discard, Stdin: strings.NewReader(`[{"draft_id":"41","confirm_send":"41","idempotency_key":"one"},{"draft_id":"41","confirm_send":"41","idempotency_key":"two"}]`), checkpoint: func(model.State) error {
		saves++
		if saves == 1 {
			return nil
		}
		return fmt.Errorf("synthetic disk unavailable")
	}}
	code := a.run(args)
	if code != 4 || len(sent()) != 1 || !strings.Contains(out.String(), `"code":"imap_send_uncertain"`) || !strings.Contains(out.String(), `"retryable":false`) {
		t.Fatalf("exit=%d sends=%d out=%s", code, len(sent()), out.String())
	}
}

func TestDraftCommandsRejectIDFromAnotherMailbox(t *testing.T) {
	for _, cmd := range [][]string{{"draft", "get"}, {"draft", "update"}, {"draft", "delete"}, {"message", "send"}, {"message", "send-many"}} {
		t.Run(strings.Join(cmd, "-"), func(t *testing.T) {
			s := &workflowServer{}
			args := imapCommandArgs(t, newWorkflowServer(t, s))
			sent := captureWorkflowSMTP(t, args)
			args = append(args, cmd...)
			input := ""
			if cmd[1] == "send-many" {
				args = append(args, "--stdin")
				input = `[{"draft_id":"imap:Archive:41","confirm_send":"imap:Archive:41"}]`
			} else {
				args = append(args, "--draft-id", "imap:Archive:41")
				if cmd[1] == "send" {
					args = append(args, "--confirm-send", "imap:Archive:41")
				}
			}
			code, out := runWorkflow(t, args, input)
			cmds, appended := s.snapshot()
			if code == 0 || !strings.Contains(out, "validation_error") || len(sent()) != 0 || len(appended) != 0 || strings.Contains(strings.Join(cmds, "\n"), "UID STORE") {
				t.Fatalf("exit=%d out=%s sends=%d appends=%d commands=%v", code, out, len(sent()), len(appended), cmds)
			}
		})
	}
}

func TestThreadHeadersParticipateInSendIdempotency(t *testing.T) {
	s := &workflowServer{}
	args := imapCommandArgs(t, newWorkflowServer(t, s))
	sent := captureWorkflowSMTP(t, args)
	args = append(args, "message", "send", "--draft-id", "41", "--confirm-send", "41", "--idempotency-key", "thread-key")
	if code, out := runWorkflow(t, args, ""); code != 0 {
		t.Fatalf("first exit=%d out=%s", code, out)
	}
	s.mu.Lock()
	s.raw = strings.ReplaceAll(s.raw, "<parent@example.invalid>", "<new-parent@example.invalid>")
	s.mu.Unlock()
	code, out := runWorkflow(t, args, "")
	if code != 6 || !strings.Contains(out, "idempotency_conflict") || len(sent()) != 1 {
		t.Fatalf("replay exit=%d sends=%d out=%s", code, len(sent()), out)
	}
}
