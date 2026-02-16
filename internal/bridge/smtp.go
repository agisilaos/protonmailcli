package bridge

import (
	"fmt"
	"net/smtp"
	"sort"
	"strings"
)

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
}

type SendInput struct {
	From         string
	To           []string
	Subject      string
	Body         string
	ExtraHeaders map[string]string
}

func Send(cfg SMTPConfig, in SendInput) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	headers := []string{
		fmt.Sprintf("From: %s", in.From),
		fmt.Sprintf("To: %s", strings.Join(in.To, ", ")),
		fmt.Sprintf("Subject: %s", in.Subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
	}
	if len(in.ExtraHeaders) > 0 {
		keys := make([]string, 0, len(in.ExtraHeaders))
		for k := range in.ExtraHeaders {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			headers = append(headers, fmt.Sprintf("%s: %s", k, in.ExtraHeaders[k]))
		}
	}
	msg := strings.Join(headers, "\r\n") + "\r\n\r\n" + in.Body
	var auth smtp.Auth
	if cfg.Username != "" && cfg.Password != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	return smtp.SendMail(addr, auth, in.From, in.To, []byte(msg))
}
