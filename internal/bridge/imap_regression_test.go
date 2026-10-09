package bridge

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// runIMAPExchange exercises the public client methods against a disposable wire peer.
func runIMAPExchange(t *testing.T, run func(*IMAPClient) error, reply func(string, string) string) (error, []string) {
	t.Helper()
	conn, peer := net.Pipe()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))
	commands := make(chan []string, 1)
	go func() {
		defer peer.Close()
		var seen []string
		defer func() { commands <- seen }()
		reader := bufio.NewReader(peer)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			seen = append(seen, line)
			tag, command, _ := strings.Cut(line, " ")
			response := ""
			if reply != nil {
				response = reply(tag, command)
			}
			if response == "" {
				response = tag + " OK done\r\n"
			}
			if _, err := io.WriteString(peer, response); err != nil {
				return
			}
		}
	}()
	c := &IMAPClient{conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn), tag: 1, timeout: time.Second}
	err := run(c)
	_ = conn.Close()
	return err, <-commands
}

func TestIMAPRejectsUnsafeSelectorsBeforeDispatch(t *testing.T) {
	for _, uid := range []string{"0", "-1", "4294967296", "41:42", "41,42", "*", "41 UID STORE 1 +FLAGS (x)", "41\r\nA9999 UID STORE 1 +FLAGS (x)"} {
		for _, method := range []string{"get", "delete", "keyword", "move"} {
			t.Run(method+"/"+uid, func(t *testing.T) {
				err, sent := runIMAPExchange(t, func(c *IMAPClient) error {
					switch method {
					case "get":
						_, err := c.GetDraft(uid)
						return err
					case "delete":
						return c.DeleteDraft(uid)
					case "keyword":
						return c.SetKeyword("INBOX", uid, "safe", true)
					default:
						return c.MoveUID("INBOX", uid, "Drafts")
					}
				}, nil)
				if err == nil || len(sent) != 0 {
					t.Fatalf("invalid UID dispatched: error=%v commands=%q", err, sent)
				}
			})
		}
	}
	for _, tc := range []struct {
		name string
		run  func(*IMAPClient) error
	}{
		{"mailbox", func(c *IMAPClient) error { _, err := c.ListMessages("INBOX\r\nA9999 NOOP", "ALL"); return err }},
		{"destination", func(c *IMAPClient) error { return c.MoveUID("INBOX", "41", "Drafts\nNOOP") }},
		{"criteria", func(c *IMAPClient) error { _, err := c.SearchUIDs("INBOX", "ALL\r\nA9999 NOOP"); return err }},
		{"quote", func(c *IMAPClient) error { _, err := c.SearchUIDs("INBOX", `TEXT "unfinished`); return err }},
		{"literal", func(c *IMAPClient) error { _, err := c.SearchUIDs("INBOX", "TEXT {4}"); return err }},
		{"keyword", func(c *IMAPClient) error { return c.SetKeyword("INBOX", "41", "x)\r\nA9999 NOOP", true) }},
		{"keyword flags", func(c *IMAPClient) error { return c.SetKeyword("INBOX", "41", `\Deleted`, true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err, sent := runIMAPExchange(t, tc.run, nil)
			if err == nil || len(sent) != 0 {
				t.Fatalf("unsafe selector dispatched: error=%v commands=%q", err, sent)
			}
		})
	}
}

func TestIMAPQuotesMailboxBackslashesAndQuotes(t *testing.T) {
	err, sent := runIMAPExchange(t, func(c *IMAPClient) error { _, err := c.SearchUIDs(`Work\2026 "mail"`, `TEXT "a\\b\"c"`); return err }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0] != `A0001 SELECT "Work\\2026 \"mail\""` {
		t.Fatalf("mailbox incorrectly escaped: %q", sent)
	}
}

func syntheticMessageReply(raw string) func(string, string) string {
	return func(tag, command string) string {
		switch {
		case strings.HasPrefix(command, "UID SEARCH "):
			return "* SEARCH 41\r\n" + tag + " OK searched\r\n"
		case strings.HasPrefix(command, "UID FETCH "):
			return fmt.Sprintf("* 1 FETCH (UID 41 FLAGS () BODY[] {%d}\r\n%s)\r\n%s OK fetched\r\n", len(raw), raw, tag)
		default:
			return ""
		}
	}
}

func TestDeleteDraftDeletesOnlyRequestedUID(t *testing.T) {
	deleted := map[string]bool{"99": true}
	expunged := map[string]bool{}
	err, sent := runIMAPExchange(t, func(c *IMAPClient) error { return c.DeleteDraft("41") }, func(tag, command string) string {
		switch command {
		case "CAPABILITY":
			return "* CAPABILITY IMAP4rev1 UIDPLUS\r\n" + tag + " OK capabilities\r\n"
		case `UID STORE 41 +FLAGS.SILENT (\Deleted)`:
			deleted["41"] = true
		case "UID EXPUNGE 41":
			if deleted["41"] {
				expunged["41"] = true
			}
		case "EXPUNGE":
			for uid := range deleted {
				expunged[uid] = true
			}
		default:
			if strings.HasPrefix(command, "UID STORE") {
				return tag + " BAD invalid flags\r\n"
			}
		}
		return ""
	})
	if err != nil {
		t.Fatalf("valid draft delete failed: %v; commands=%q", err, sent)
	}
	if !expunged["41"] || expunged["99"] {
		t.Fatalf("unexpected deletions: %v; commands=%q", expunged, sent)
	}
}

func TestDeleteDraftWithoutUIDPLUSDoesNotMarkOrExpunge(t *testing.T) {
	err, sent := runIMAPExchange(t, func(c *IMAPClient) error { return c.DeleteDraft("41") }, nil)
	if err == nil {
		t.Fatal("delete without scoped expunge support succeeded")
	}
	for _, command := range sent {
		if strings.Contains(command, "STORE") || strings.Contains(command, "EXPUNGE") {
			t.Fatalf("mutated without safe deletion support: %q", sent)
		}
	}
}

func TestListMessagesPreservesUnreadState(t *testing.T) {
	seen := false
	raw := "Subject: unread\r\n\r\nmessage"
	err, sent := runIMAPExchange(t, func(c *IMAPClient) error {
		for i := 0; i < 2; i++ {
			messages, err := c.ListMessages("INBOX", "UNSEEN")
			if err != nil {
				return err
			}
			if len(messages) != 1 {
				return fmt.Errorf("read-only search %d returned %d unread messages, want 1", i+1, len(messages))
			}
		}
		return nil
	}, func(tag, command string) string {
		if strings.HasPrefix(command, "UID SEARCH") && seen {
			return "* SEARCH\r\n" + tag + " OK searched\r\n"
		}
		if strings.HasPrefix(command, "UID FETCH") && !strings.Contains(command, "BODY.PEEK[]") {
			seen = true
		}
		return syntheticMessageReply(raw)(tag, command)
	})
	if err != nil || seen {
		t.Fatalf("read-only fetch marked message seen: error=%v seen=%v commands=%q", err, seen, sent)
	}
}

func TestGetDraftDistinguishesMissingMessageFromBackendFailure(t *testing.T) {
	for _, status := range []string{"OK", "NO", "BAD"} {
		t.Run(status, func(t *testing.T) {
			err, _ := runIMAPExchange(t, func(c *IMAPClient) error { _, err := c.GetDraft("41"); return err }, func(tag, command string) string {
				if strings.HasPrefix(command, "UID FETCH") {
					return tag + " " + status + " synthetic reply\r\n"
				}
				return ""
			})
			if err == nil || errors.Is(err, ErrMessageNotFound) != (status == "OK") {
				t.Fatalf("status %s: incorrect absence classification: %v", status, err)
			}
		})
	}
}

func TestMessagesDecodeMIMECharsetsAndSubjects(t *testing.T) {
	for _, tc := range []struct {
		name, raw, subject, body string
		wantError                bool
	}{
		{name: "latin1", raw: "Subject: =?UTF-8?Q?Gr=C3=BC=C3=9Fe?=\r\nContent-Type: text/plain; charset=ISO-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nGr=FC=DFe", subject: "Grüße", body: "Grüße"},
		{name: "windows1252 multipart", raw: "Subject: =?ISO-8859-1?Q?Gr=FC=DFe?=\r\nContent-Type: multipart/alternative; boundary=part\r\n\r\n--part\r\nContent-Type: text/plain; charset=windows-1252\r\nContent-Transfer-Encoding: base64\r\n\r\ngCBwcmljZQ==\r\n--part--\r\n", subject: "Grüße", body: "€ price"},
		{name: "utf8", raw: "Subject: =?UTF-8?B?R3LDvMOfZQ==?=\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nGrüße", subject: "Grüße", body: "Grüße"},
		{name: "unknown charset", raw: "Subject: unknown\r\nContent-Type: text/plain; charset=not-a-charset\r\n\r\nbody", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var messages []DraftMessage
			err, _ := runIMAPExchange(t, func(c *IMAPClient) error { var err error; messages, err = c.ListMessages("INBOX", "ALL"); return err }, syntheticMessageReply(tc.raw))
			if tc.wantError {
				if err == nil {
					t.Fatal("unsupported charset silently accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || messages[0].Subject != tc.subject || messages[0].Body != tc.body {
				t.Fatalf("decoded message=%+v; want subject %q body %q", messages, tc.subject, tc.body)
			}
		})
	}
}

func TestMessagesExcludeAttachmentsFromBody(t *testing.T) {
	for _, attachmentHeader := range []string{
		"Content-Type: text/plain\r\nContent-Disposition: attachment; filename=notes.txt\r\n",
		"Content-Type: text/plain; name=notes.txt\r\n",
		"Content-Type: multipart/alternative; boundary=attached\r\nContent-Disposition: attachment\r\n",
	} {
		raw := "Subject: body choice\r\nContent-Type: multipart/mixed; boundary=outer\r\n\r\n--outer\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Actual body</p>\r\n--outer\r\n" + attachmentHeader + "\r\nATTACHMENT CONTENT\r\n"
		if strings.Contains(attachmentHeader, "multipart/") {
			raw += "--attached\r\nContent-Type: text/plain\r\n\r\nNESTED ATTACHMENT CONTENT\r\n--attached--\r\n"
		}
		raw += "--outer--\r\n"
		t.Run(attachmentHeader, func(t *testing.T) {
			var messages []DraftMessage
			err, _ := runIMAPExchange(t, func(c *IMAPClient) error { var err error; messages, err = c.ListMessages("INBOX", "ALL"); return err }, syntheticMessageReply(raw))
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || messages[0].Body != "<p>Actual body</p>" {
				t.Fatalf("attachment replaced body: %+v", messages)
			}
		})
	}
}

func TestMessagesPreserveBodyWhitespace(t *testing.T) {
	const body = "    indented line\r\n\tsecond line  \r\n\r\n"
	for _, tc := range []struct{ name, raw string }{
		{"no content type", "Subject: whitespace\r\n\r\n" + body},
		{"plain utf8", "Subject: whitespace\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body},
		{"multipart", "Subject: whitespace\r\nContent-Type: multipart/mixed; boundary=p\r\n\r\n--p\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body + "\r\n--p--\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var messages []DraftMessage
			err, _ := runIMAPExchange(t, func(c *IMAPClient) error { var err error; messages, err = c.ListMessages("INBOX", "ALL"); return err }, syntheticMessageReply(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || messages[0].Body != body {
				t.Fatalf("body whitespace changed: %+v; want %q", messages, body)
			}
		})
	}
}

func TestIMAPDiscoveredMailboxQuotesRoundTrip(t *testing.T) {
	const mailbox = `Projects\2026 "mail"`
	const raw = "Subject: discovered\r\n\r\nbody"
	var boxes []string
	err, sent := runIMAPExchange(t, func(c *IMAPClient) error {
		var err error
		boxes, err = c.ListMailboxes()
		if err != nil {
			return err
		}
		_, err = c.GetDraft("41")
		return err
	}, func(tag, command string) string {
		if strings.HasPrefix(command, "LIST ") {
			return `* LIST (\Drafts) "/" "Projects\\2026 \"mail\""` + "\r\n" + tag + " OK listed\r\n"
		}
		return syntheticMessageReply(raw)(tag, command)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 1 || boxes[0] != mailbox {
		t.Fatalf("mailbox name changed: %q", boxes)
	}
	if len(sent) != 4 || sent[2] != `A0003 SELECT "Projects\\2026 \"mail\""` {
		t.Fatalf("discovered mailbox selected incorrectly: %q", sent)
	}
}

func TestGetDraftExistingEmptyBodyIsNotMissing(t *testing.T) {
	for _, fieldsOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(fieldsOnly), func(t *testing.T) {
			err, _ := runIMAPExchange(t, func(c *IMAPClient) error { _, err := c.GetDraft("41"); return err }, func(tag, command string) string {
				if fieldsOnly && strings.HasPrefix(command, "UID FETCH") {
					return "* 1 FETCH (UID 41 FLAGS ())\r\n" + tag + " OK fetched\r\n"
				}
				return syntheticMessageReply("")(tag, command)
			})
			if err == nil || errors.Is(err, ErrMessageNotFound) {
				t.Fatalf("existing empty message must fail decoding, not become missing: %v", err)
			}
		})
	}
}

func TestMessagesKeepUsableBodyWhenAlternativeCannotDecode(t *testing.T) {
	const good = "Content-Type: text/plain; charset=UTF-8\r\n\r\nusable plain body\r\n"
	const bad = "Content-Type: text/html; charset=not-a-charset\r\n\r\n<p>other body</p>\r\n"
	for _, parts := range []string{good + "--part\r\n" + bad, bad + "--part\r\n" + good} {
		raw := "Subject: alternatives\r\nContent-Type: multipart/alternative; boundary=part\r\n\r\n--part\r\n" + parts + "--part--\r\n"
		var messages []DraftMessage
		err, _ := runIMAPExchange(t, func(c *IMAPClient) error { var err error; messages, err = c.ListMessages("INBOX", "ALL"); return err }, syntheticMessageReply(raw))
		if err != nil {
			t.Fatalf("usable plain body hidden by unsupported alternative: %v", err)
		}
		if len(messages) != 1 || messages[0].Body != "usable plain body" {
			t.Fatalf("incorrect body selection: %+v", messages)
		}
	}
}

func TestIMAPKeywordAtomSearchAndMutation(t *testing.T) {
	for _, keyword := range []string{"work", "phase}2"} {
		t.Run(keyword, func(t *testing.T) {
			err, sent := runIMAPExchange(t, func(c *IMAPClient) error {
				if err := c.SetKeyword("INBOX", "41", keyword, true); err != nil {
					return err
				}
				_, err := c.SearchUIDs("INBOX", "KEYWORD "+keyword)
				return err
			}, nil)
			if err != nil {
				t.Fatalf("valid keyword atom rejected: %v; commands=%q", err, sent)
			}
			if len(sent) != 4 || sent[3] != "A0004 UID SEARCH KEYWORD "+keyword {
				t.Fatalf("incorrect keyword search: %q", sent)
			}
		})
	}
	for _, criteria := range []string{`KEYWORD "work"`, `KEYWORD phase{2`, `KEYWORD \Seen`} {
		err, sent := runIMAPExchange(t, func(c *IMAPClient) error { _, err := c.SearchUIDs("INBOX", criteria); return err }, nil)
		if err == nil || len(sent) != 0 {
			t.Fatalf("invalid keyword search dispatched: %q error=%v commands=%q", criteria, err, sent)
		}
	}
}

func TestDeleteDraftSupportsIMAP4rev2ScopedExpunge(t *testing.T) {
	var expunged bool
	err, sent := runIMAPExchange(t, func(c *IMAPClient) error { return c.DeleteDraft("41") }, func(tag, command string) string {
		switch command {
		case "CAPABILITY":
			return "* CAPABILITY IMAP4rev2\r\n" + tag + " OK capabilities\r\n"
		case "UID EXPUNGE 41":
			expunged = true
		case "EXPUNGE":
			return tag + " BAD mailbox-wide expunge forbidden\r\n"
		}
		return ""
	})
	if err != nil || !expunged {
		t.Fatalf("IMAP4rev2 scoped deletion rejected: error=%v commands=%q", err, sent)
	}
}

func TestGetDraftIgnoresUnrelatedFetchResponses(t *testing.T) {
	const raw = "Subject: requested\r\n\r\nrequested body"
	const other = "Subject: unrelated\r\n\r\nunrelated body"
	requested := fmt.Sprintf("* 1 FETCH (UID 41 FLAGS () BODY[] {%d}\r\n%s)\r\n", len(raw), raw)
	unrelated := fmt.Sprintf("* 2 FETCH (UID 99 FLAGS (\\Seen) BODY[] {%d}\r\n%s)\r\n", len(other), other)
	for _, tc := range []struct {
		name, fetch string
		missing     bool
	}{
		{"absent with unrelated flags", "* 2 FETCH (UID 99 FLAGS (\\Seen))\r\n", true},
		{"absent with no UID notification", "* 2 FETCH (FLAGS (\\Seen))\r\n", true},
		{"absent with UID-like keywords", "* 2 FETCH (FLAGS (UID 41))\r\n", true},
		{"absent with unrelated body", unrelated, true},
		{"present followed by unrelated flags", requested + "* 2 FETCH (UID 99 FLAGS (\\Seen))\r\n", false},
		{"present followed by unrelated body", requested + unrelated, false},
		{"UID after literal", fmt.Sprintf("* 1 FETCH (BODY[] {%d}\r\n%s UID 41 FLAGS ())\r\n", len(raw), raw), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var message DraftMessage
			err, _ := runIMAPExchange(t, func(c *IMAPClient) error { var err error; message, err = c.GetDraft("41"); return err }, func(tag, command string) string {
				if strings.HasPrefix(command, "UID FETCH") {
					return tc.fetch + tag + " OK fetched\r\n"
				}
				return ""
			})
			if tc.missing {
				if !errors.Is(err, ErrMessageNotFound) {
					t.Fatalf("unrelated response changed requested UID absence: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if message.Body != "requested body" || message.Subject != "requested" || len(message.Flags) != 0 {
				t.Fatalf("requested message replaced by unrelated FETCH: %+v", message)
			}
		})
	}
}

func TestGetDraftAcceptsCaseInsensitiveFetchItems(t *testing.T) {
	const raw = "Subject: case\r\n\r\nbody"
	for _, keyword := range []string{"FETCH", "fetch", "fEtCh"} {
		t.Run(keyword, func(t *testing.T) {
			var message DraftMessage
			err, _ := runIMAPExchange(t, func(c *IMAPClient) error { var err error; message, err = c.GetDraft("41"); return err }, func(tag, command string) string {
				if strings.HasPrefix(command, "UID FETCH") {
					return fmt.Sprintf("* 1 %s (uid 41 flags (work) BODY[] {%d}\r\n%s)\r\n* 2 %s (flags (UID 41))\r\n%s OK fetched\r\n", keyword, len(raw), raw, keyword, tag)
				}
				return ""
			})
			if err != nil || message.Body != "body" || len(message.Flags) != 1 || message.Flags[0] != "work" {
				t.Fatalf("case-insensitive FETCH items changed result: message=%+v error=%v", message, err)
			}
		})
	}
}
