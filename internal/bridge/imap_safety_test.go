package bridge

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func TestAppendDraftUsesAssignedUID(t *testing.T) {
	for _, response := range []struct{ name, completion, want string }{
		{"assigned", "OK [APPENDUID 7 41] appended", "41"},
		{"missing", "OK appended", ""},
		{"range", "OK [APPENDUID 7 41:42] appended", ""},
		{"zero", "OK [APPENDUID 7 0] appended", ""},
		{"invalid validity", "OK [APPENDUID 0 41] appended", ""},
		{"overflow", "OK [APPENDUID 7 4294967296] appended", ""},
		{"truncated", "", ""},
	} {
		t.Run(response.name, func(t *testing.T) {
			transcript := "A0001 OK list\r\nA0002 OK selected\r\n+ ready\r\n"
			if response.completion != "" {
				transcript += "A0003 " + response.completion + "\r\n"
			}
			// Old code picks another client's UID from this later mailbox snapshot.
			if response.completion != "" {
				transcript += "A0004 OK selected\r\n* SEARCH 41 42\r\nA0005 OK searched\r\n"
			}
			var sent bytes.Buffer
			c := &IMAPClient{r: bufio.NewReader(strings.NewReader(transcript)), w: bufio.NewWriter(&sent), tag: 1}
			uid, err := c.AppendDraft("Subject: synthetic\r\n\r\nbody")
			if uid != response.want {
				t.Fatalf("uid=%q want=%q", uid, response.want)
			}
			if response.want == "" {
				if !errors.Is(err, ErrAppendUncertain) {
					t.Fatalf("expected uncertainty, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(sent.String(), "SEARCH ALL") {
				t.Fatal("queried unrelated draft identities")
			}
		})
	}
}

func TestLoginDoesNotExposeCredentials(t *testing.T) {
	for _, status := range []string{"OK", "NO"} {
		t.Run(status, func(t *testing.T) {
			const user, pass = "sentinel-user", "sentinel-password"
			var sent bytes.Buffer
			c := &IMAPClient{r: bufio.NewReader(strings.NewReader("A0001 " + status + " " + user + " " + pass + "\r\nA0002 OK done\r\n")), w: bufio.NewWriter(&sent), tag: 1, debug: true}
			log, err := os.CreateTemp(t.TempDir(), "debug")
			if err != nil {
				t.Fatal(err)
			}
			original := os.Stderr
			os.Stderr = log
			t.Cleanup(func() { os.Stderr = original; log.Close() })
			loginErr := c.login(user, pass)
			if status == "NO" && loginErr == nil {
				t.Fatal("expected auth failure")
			}
			if status == "OK" && loginErr != nil {
				t.Fatal(loginErr)
			}
			if err := c.simple("NOOP"); err != nil {
				t.Fatal(err)
			}
			os.Stderr = original
			if _, err := log.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(log)
			if err != nil {
				t.Fatal(err)
			}
			output := string(data)
			if loginErr != nil {
				output += loginErr.Error()
			}
			if strings.Contains(output, user) || strings.Contains(output, pass) {
				t.Fatal("authentication material disclosed")
			}
			if !strings.Contains(output, "NOOP") {
				t.Fatal("non-authentication diagnostics disabled")
			}
			if !strings.Contains(sent.String(), pass) {
				t.Fatal("wire authentication changed")
			}
		})
	}
}

func TestListMessagesRejectsIncompleteFetches(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failAt  int
		failure string
	}{
		{"first", 0, "NO unavailable\r\n"}, {"middle", 1, "NO unavailable\r\n"}, {"last", 2, "BAD invalid\r\n"},
		{"missing literal", 1, "OK fetched\r\n"}, {"truncated", 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transcript := "A0001 OK selected\r\n* SEARCH 41 42 43\r\nA0002 OK searched\r\n"
			for i := 0; i < 3; i++ {
				tag := fmt.Sprintf("A%04d", i+3)
				if i == tc.failAt {
					if tc.failure != "" {
						transcript += tag + " " + tc.failure
					}
					break
				}
				raw := "Subject: synthetic\r\n\r\nbody"
				transcript += fmt.Sprintf("* 1 FETCH (UID %d BODY[] {%d}\r\n%s)\r\n%s OK fetched\r\n", 41+i, len(raw), raw, tag)
			}
			var sent bytes.Buffer
			c := &IMAPClient{r: bufio.NewReader(strings.NewReader(transcript)), w: bufio.NewWriter(&sent), tag: 1}
			messages, err := c.ListMessages("INBOX", "ALL")
			if err == nil || messages != nil {
				t.Fatalf("partial success: messages=%v err=%v", messages, err)
			}
			if n := strings.Count(sent.String(), "UID FETCH"); n != tc.failAt+1 {
				t.Fatalf("fetches=%d want=%d", n, tc.failAt+1)
			}
		})
	}
}

func TestListMessagesCompleteAndEmpty(t *testing.T) {
	for _, count := range []int{0, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			transcript := "A0001 OK selected\r\n* SEARCH"
			for i := 0; i < count; i++ {
				transcript += fmt.Sprintf(" %d", 41+i)
			}
			transcript += "\r\nA0002 OK searched\r\n"
			for i := 0; i < count; i++ {
				raw := "Subject: synthetic\r\n\r\nbody"
				transcript += fmt.Sprintf("* 1 FETCH (UID %d BODY[] {%d}\r\n%s)\r\nA%04d OK fetched\r\n", 41+i, len(raw), raw, i+3)
			}
			var sent bytes.Buffer
			c := &IMAPClient{r: bufio.NewReader(strings.NewReader(transcript)), w: bufio.NewWriter(&sent), tag: 1}
			messages, err := c.ListMessages("INBOX", "ALL")
			if err != nil || len(messages) != count {
				t.Fatalf("messages=%v err=%v", messages, err)
			}
		})
	}
}
