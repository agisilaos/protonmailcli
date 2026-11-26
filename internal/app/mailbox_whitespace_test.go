package app

import (
	"strconv"
	"strings"
	"testing"
)

func TestMailboxResolvePreservesWhitespaceWhenCaseFolding(t *testing.T) {
	for _, tc := range []struct {
		name, query, want string
		boxes             []string
		ambiguous         bool
	}{
		{"trailing space", "PROJECT ", "Project ", []string{"Project", "Project "}, false},
		{"leading space", " PROJECT", " Project", []string{"Project", " Project"}, false},
		{"exact space", "Project ", "Project ", []string{"Project", "Project "}, false},
		{"unique convenience", " PROJECT  ", "Project", []string{"Project"}, false},
		{"ambiguous convenience", " PROJECT  ", "", []string{"Project", "Project "}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &workflowServer{boxes: tc.boxes}
			args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "mailbox", "resolve", "--name", tc.query)
			code, out := runWorkflow(t, args, "")
			if tc.ambiguous {
				if code != 2 || !strings.Contains(out, "ambiguous") {
					t.Fatalf("ambiguous whitespace convenience selected a mailbox: exit=%d out=%s", code, out)
				}
				return
			}
			if code != 0 || regressionData(t, out)["mailbox"].(map[string]any)["name"] != tc.want {
				t.Fatalf("resolve %q must preserve %q: exit=%d out=%s", tc.query, tc.want, code, out)
			}
		})
	}
}

func TestMessageSearchPreservesResolvedServerMailbox(t *testing.T) {
	for _, tc := range []struct {
		name, query, want string
		boxes             []string
		ambiguous         bool
	}{
		{"exact space", "Project ", "Project ", []string{"Project", "Project "}, false},
		{"casefold space", "PROJECT ", "Project ", []string{"Project", "Project "}, false},
		{"leading space", " PROJECT", " Project", []string{"Project", " Project"}, false},
		{"unique convenience", " PROJECT  ", "Project", []string{"Project"}, false},
		{"ambiguous convenience", " PROJECT  ", "", []string{"Project", "Project "}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &workflowServer{boxes: tc.boxes}
			args := append(imapCommandArgs(t, newWorkflowServer(t, s)), "search", "messages", "--mailbox", tc.query)
			code, out := runWorkflow(t, args, "")
			commands, _ := s.snapshot()
			if tc.ambiguous {
				if code != 2 || !strings.Contains(out, "ambiguous") || workflowOpenedMailbox(commands, "") {
					t.Fatalf("ambiguous search accessed a mailbox: exit=%d out=%s commands=%v", code, out, commands)
				}
				return
			}
			if code != 0 {
				t.Fatalf("search %q: exit=%d out=%s", tc.query, code, out)
			}
			data := regressionData(t, out)
			messages := data["messages"].([]any)
			if data["mailbox"] != tc.want || len(messages) != 1 || messages[0].(map[string]any)["id"] != "imap:"+tc.want+":41" {
				t.Fatalf("search must return exact server mailbox %q: %s", tc.want, out)
			}
			selected := false
			for _, command := range commands {
				if strings.HasSuffix(command, " EXAMINE "+strconv.Quote(tc.want)) {
					selected = true
				}
			}
			if !selected {
				t.Fatalf("search did not select exact server mailbox %q: %v", tc.want, commands)
			}
		})
	}
}

func TestEmptySearchMailboxNeverFallsBackToInbox(t *testing.T) {
	for _, action := range []string{"messages", "drafts"} {
		t.Run(action, func(t *testing.T) {
			s := &workflowServer{}
			base := imapCommandArgs(t, newWorkflowServer(t, s))
			code, out := runWorkflow(t, append(base, "search", action, "--mailbox", ""), "")
			commands, _ := s.snapshot()
			if code != 2 || !strings.Contains(out, "validation_error") || len(commands) > 0 {
				t.Fatalf("empty mailbox: exit=%d commands=%v output=%s", code, commands, out)
			}
		})
	}
}
