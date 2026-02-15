package bridge

import (
	"fmt"
	"net/smtp"
	"strings"
)

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
}

type SendInput struct {
	From    string
	To      []string
	Subject string
	Body    string
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
	msg := strings.Join(headers, "\r\n") + "\r\n\r\n" + in.Body
	var auth smtp.Auth
	if cfg.Username != "" && cfg.Password != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	return smtp.SendMail(addr, auth, in.From, in.To, []byte(msg))
}
