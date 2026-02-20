package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPrintSuccessJSONEnvelope(t *testing.T) {
	var out bytes.Buffer
	start := time.Now().Add(-10 * time.Millisecond)
	err := PrintSuccess(&out, ModeJSON, map[string]any{"k": "v"}, "default", "req_1", start)
	if err != nil {
		t.Fatalf("print success: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("expected ok=true: %+v", env)
	}
	if env.Meta.RequestID != "req_1" {
		t.Fatalf("unexpected request id: %s", env.Meta.RequestID)
	}
	if env.Meta.DurationMS < 0 {
		t.Fatalf("unexpected negative duration: %d", env.Meta.DurationMS)
	}
}

func TestPrintErrorPlainMode(t *testing.T) {
	var out bytes.Buffer
	start := time.Now()
	err := PrintError(&out, ModePlain, "validation_error", "bad\tvalue", "hint\ttext", "usage", false, "default", "req_2", start)
	if err != nil {
		t.Fatalf("print error: %v", err)
	}
	line := strings.TrimSpace(out.String())
	if !strings.Contains(line, "ok\tfalse\terror\tvalidation_error") {
		t.Fatalf("unexpected plain error line: %q", line)
	}
	if strings.Contains(line, "bad\tvalue") || strings.Contains(line, "hint\ttext") {
		t.Fatalf("tabs should be sanitized in plain output: %q", line)
	}
}

func TestPrintErrorHumanMode(t *testing.T) {
	var out bytes.Buffer
	start := time.Now()
	err := PrintError(&out, ModeHuman, "runtime_error", "boom", "", "runtime", false, "", "req_3", start)
	if err != nil {
		t.Fatalf("print error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "error: boom (runtime_error)" {
		t.Fatalf("unexpected human output: %q", out.String())
	}
}
