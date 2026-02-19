package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type Mode string

const (
	ModeHuman Mode = "human"
	ModeJSON  Mode = "json"
	ModePlain Mode = "plain"
)

type Envelope struct {
	OK       bool        `json:"ok"`
	Data     interface{} `json:"data,omitempty"`
	Error    *ErrBody    `json:"error,omitempty"`
	Meta     Meta        `json:"meta"`
	Warnings []string    `json:"warnings,omitempty"`
}

type Meta struct {
	RequestID  string `json:"requestId"`
	Profile    string `json:"profile,omitempty"`
	DurationMS int64  `json:"durationMs"`
	Timestamp  string `json:"timestamp"`
}

type ErrBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
	Category  string `json:"category,omitempty"`
	Retryable bool   `json:"retryable"`
}

func PrintSuccess(w io.Writer, mode Mode, data interface{}, profile, requestID string, start time.Time) error {
	env := Envelope{OK: true, Data: data, Meta: meta(profile, requestID, start)}
	return printEnvelope(w, mode, env)
}

func PrintError(w io.Writer, mode Mode, code, msg, hint, category string, retryable bool, profile, requestID string, start time.Time) error {
	env := Envelope{OK: false, Error: &ErrBody{Code: code, Message: msg, Hint: hint, Category: category, Retryable: retryable}, Meta: meta(profile, requestID, start)}
	return printEnvelope(w, mode, env)
}

func printEnvelope(w io.Writer, mode Mode, env Envelope) error {
	switch mode {
	case ModeJSON:
		b, err := json.Marshal(env)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(b))
		return err
	case ModePlain:
		if env.OK {
			if env.Data == nil {
				_, err := fmt.Fprintln(w, "ok\ttrue")
				return err
			}
			b, err := json.Marshal(env.Data)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(w, "ok\ttrue\tdata\t%s\n", string(b))
			return err
		}
		hint := ""
		if env.Error != nil {
			hint = strings.ReplaceAll(env.Error.Hint, "\t", " ")
		}
		_, err := fmt.Fprintf(w, "ok\tfalse\terror\t%s\tmessage\t%s\thint\t%s\n", env.Error.Code, strings.ReplaceAll(env.Error.Message, "\t", " "), hint)
		return err
	default:
		if env.OK {
			_, err := fmt.Fprintln(w, "ok")
			return err
		}
		_, err := fmt.Fprintf(w, "error: %s (%s)\n", env.Error.Message, env.Error.Code)
		return err
	}
}

func meta(profile, requestID string, start time.Time) Meta {
	return Meta{
		RequestID:  requestID,
		Profile:    profile,
		DurationMS: time.Since(start).Milliseconds(),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
}
