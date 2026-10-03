package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashCompletionDepth(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "completion.bash")
	if err := os.WriteFile(file, []byte(bashCompletion()), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"message", ""}, "send send-many get follow-up"},
		{[]string{"message", "send", ""}, ""},
		{[]string{"--json", "message", ""}, "send send-many get follow-up"},
		{[]string{"--config", "message", "draft", ""}, "create create-many update get list delete"},
		{[]string{"--config", ""}, ""},
		{[]string{"message", "--", ""}, ""},
		{[]string{"bridge", "account", ""}, "list use"},
		{[]string{"message", "se"}, "send send-many"},
		{[]string{"--bogus", ""}, ""},
		{[]string{"message", "--json", ""}, ""},
	}
	for _, tc := range cases {
		script := `source "$1"; shift; COMP_WORDS=("$@"); COMP_CWORD=$((${#COMP_WORDS[@]}-1)); _protonmailcli_completions; printf '%s\n' "${COMPREPLY[@]}"`
		args := append([]string{"--noprofile", "--norc", "-c", script, "test", file, "protonmailcli"}, tc.args...)
		out, err := exec.Command(bash, args...).CombinedOutput()
		if err != nil || strings.Join(strings.Fields(string(out)), " ") != tc.want {
			t.Fatalf("%v: %v %q want %q", tc.args, err, out, tc.want)
		}
	}
}
