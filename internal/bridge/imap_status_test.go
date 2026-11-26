package bridge

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestIMAPCommandStatusIsCaseInsensitive(t *testing.T) {
	for _, method := range []string{"preview", "select", "noop", "fetch"} {
		for _, status := range []string{"ok", "oK", "OK", "no", "No", "NO", "bad", "BaD", "BAD"} {
			t.Run(method+"/"+status, func(t *testing.T) {
				var sent bytes.Buffer
				tag := "A0001"
				transcript := ""
				if method == "preview" {
					transcript = "* LIST (\\Drafts) NIL Brouillons\r\nA0001 OK listed\r\n"
					tag = "A0002"
				}
				if method == "fetch" {
					raw := "Subject: fixture\r\n\r\nfixture body"
					transcript = fmt.Sprintf("A0001 OK selected\r\n* 1 FETCH (UID 41 BODY[] {%d}\r\n%s)\r\n", len(raw), raw)
					tag = "A0002"
				}
				transcript += fmt.Sprintf("* 3 EXISTS\r\n%s %s fixture response\r\n", tag, status)
				c := &IMAPClient{r: bufio.NewReader(strings.NewReader(transcript)), w: bufio.NewWriter(&sent), tag: 1}
				var err error
				switch method {
				case "preview":
					err = c.ExamineDraftDestination()
				case "select":
					err = c.selectMailbox("INBOX")
				case "noop":
					err = c.simple("NOOP")
				case "fetch":
					var message DraftMessage
					message, err = c.GetMessage("INBOX", "41")
					if err == nil && (message.UID != "41" || message.Body != "fixture body") {
						t.Fatalf("fetch lost the requested message: %+v", message)
					}
				}
				if strings.EqualFold(status, "OK") {
					if err != nil {
						t.Fatalf("successful %s completion was ignored: %v", status, err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tag+" "+status+" fixture response") {
					t.Fatalf("failed %s completion was ignored: %v", status, err)
				}
				if method == "preview" && sent.String() != "A0001 LIST \"\" \"*\"\r\nA0002 EXAMINE \"Brouillons\"\r\n" {
					t.Fatalf("preview issued unexpected commands: %q", sent.String())
				}
			})
		}
	}
}

func TestIMAPFetchCompletionRequiresExactStatusToken(t *testing.T) {
	for _, prefix := range []string{"OKAY", "NOBODY", "BADLY"} {
		t.Run(prefix, func(t *testing.T) {
			raw := "Subject: fixture\r\n\r\nfixture body"
			transcript := fmt.Sprintf("A0001 OK selected\r\n* 1 FETCH (UID 41 BODY[] {%d}\r\n%s)\r\nA0002 %s not a status\r\nA0002 NO actual failure\r\n", len(raw), raw, prefix)
			var sent bytes.Buffer
			c := &IMAPClient{r: bufio.NewReader(strings.NewReader(transcript)), w: bufio.NewWriter(&sent), tag: 1}
			if _, err := c.GetMessage("INBOX", "41"); err == nil || !strings.Contains(err.Error(), "imap fetch failed: A0002 NO actual failure") {
				t.Fatalf("prefix ended FETCH before the real completion: %v", err)
			}
		})
	}
}

func TestIMAPCompletionRequiresExactTagAndStatusTokens(t *testing.T) {
	for _, status := range []string{"OK", "NO", "BAD"} {
		t.Run(status, func(t *testing.T) {
			lines := []string{
				"A00010 OK another tag",
				"a0001 OK another tag",
				" A0001 OK leading whitespace",
				"A0001 OKAY not a status",
				"A0001 NOBODY not a status",
				"A0001 BADLY not a status",
				"* 3 EXISTS",
				"A0001 " + status + " completed",
			}
			var sent bytes.Buffer
			c := &IMAPClient{r: bufio.NewReader(strings.NewReader(strings.Join(lines, "\r\n") + "\r\n")), w: bufio.NewWriter(&sent), tag: 1}
			var visited []string
			err := c.commandLines("NOOP", func(line string) error {
				visited = append(visited, line)
				return nil
			})
			if len(visited) != len(lines) {
				t.Fatalf("prefix ended the command before its completion: lines=%q err=%v", visited, err)
			}
			if status == "OK" && err != nil {
				t.Fatal(err)
			}
			if status != "OK" && (err == nil || !strings.Contains(err.Error(), "A0001 "+status+" completed")) {
				t.Fatalf("lost the actual failure completion: %v", err)
			}
		})
	}
}
