package bridge

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"sort"
	"strings"
	"time"
)

type SMTPConfig struct {
	Host        string
	Port        int
	Username    string
	Password    string
	Timeout     time.Duration
	TLSCertFile string
}

type SendInput struct {
	From         string
	To           []string
	EnvelopeTo   []string
	Subject      string
	Body         string
	ExtraHeaders map[string]string
}

func Send(cfg SMTPConfig, in SendInput) error {
	if err := ValidateMessageHeaders(in.From, in.To, in.Subject, in.ExtraHeaders); err != nil {
		return err
	}
	for _, to := range in.EnvelopeTo {
		if strings.ContainsAny(to, "\r\n") {
			return fmt.Errorf("SMTP envelope recipient must not contain CR or LF")
		}
	}
	tlsConfig := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	if cfg.TLSCertFile != "" {
		certPEM, err := os.ReadFile(cfg.TLSCertFile)
		if err != nil {
			return fmt.Errorf("read SMTP TLS certificate: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return fmt.Errorf("load system certificate roots: %w", err)
		}
		if !roots.AppendCertsFromPEM(certPEM) {
			return fmt.Errorf("SMTP TLS certificate file contains no PEM certificates")
		}
		tlsConfig.RootCAs = roots
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))
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
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if timeout < 0 {
		return fmt.Errorf("SMTP timeout must be positive")
	}
	deadline := time.Now().Add(timeout)
	conn, err := (&net.Dialer{Deadline: deadline}).Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(tlsConfig); err != nil {
			return err
		}
	} else if cfg.TLSCertFile != "" {
		return fmt.Errorf("SMTP server does not advertise STARTTLS, required with tls_cert_file")
	}
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); !ok {
			return fmt.Errorf("SMTP server does not support authentication")
		}
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(in.From); err != nil {
		return err
	}
	recipients := in.To
	if len(in.EnvelopeTo) > 0 {
		recipients = in.EnvelopeTo
	}
	for _, to := range recipients {
		if err := c.Rcpt(to); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	// DATA's successful final response is the acceptance boundary. A cleanup
	// failure after that must not tell callers to retry an accepted message.
	_ = c.Quit()
	return nil
}

// ValidateMessageHeaders rejects line injection before constructing a message
// for either SMTP delivery or IMAP storage. The body may contain newlines.
func ValidateMessageHeaders(from string, to []string, subject string, extraHeaders map[string]string) error {
	for name, value := range map[string]string{"From": from, "Subject": subject} {
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s header must not contain CR or LF", name)
		}
	}
	for _, value := range to {
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("To header must not contain CR or LF")
		}
	}
	for name, value := range extraHeaders {
		if name == "" {
			return fmt.Errorf("message header name must not be empty")
		}
		for _, c := range name {
			if c < 33 || c > 126 || c == ':' {
				return fmt.Errorf("invalid message header name %q", name)
			}
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s header must not contain CR or LF", name)
		}
	}
	return nil
}
