package bridge

import (
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"sort"
	"strings"
	"time"
)

var ErrSMTPDataUncertain = errors.New("SMTP DATA completion is unconfirmed")

type SMTPConfig struct {
	Host        string
	Port        int
	Username    string
	Password    string
	Timeout     time.Duration
	TLSCertFile string
	TLSPolicy   *TLSPolicy
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
	tlsConfig, err := cfg.TLSPolicy.configFor("SMTP", cfg.Host, cfg.TLSCertFile)
	if err != nil {
		return err
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
	// Use net/smtp for the transaction and dot encoding, but read DATA's
	// replies ourselves: net/textproto rejects the code-only spelling SMTP allows.
	id, err := c.Text.Cmd("DATA")
	if err != nil {
		return err
	}
	c.Text.StartResponse(id)
	err = readSMTPDataReply(&c.Text.Reader, 354)
	c.Text.EndResponse(id)
	if err != nil {
		return err
	}
	w := c.Text.DotWriter()
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("%w: %w", ErrSMTPDataUncertain, err)
	}
	if err := readSMTPDataReply(&c.Text.Reader, 250); err != nil {
		var reply *textproto.Error
		if errors.As(err, &reply) && reply.Code >= 400 && reply.Code < 600 {
			return err
		}
		return fmt.Errorf("%w: %w", ErrSMTPDataUncertain, err)
	}
	// DATA's successful final response is the acceptance boundary. A cleanup
	// failure after that must not tell callers to retry an accepted message.
	_ = c.Quit()
	return nil
}

// RFC 5321 section 4.2 permits a final line with just the code. Every line
// in a multiline SMTP reply must carry the same code. The caller classifies
// failures according to whether the end-of-DATA marker has been submitted.
func readSMTPDataReply(reader *textproto.Reader, expectedCode int) error {
	var code int
	var message strings.Builder
	for {
		// textproto.ReadLine accepts a fragment at EOF and removes either LF or
		// CRLF. SMTP replies are definitive only after their complete CRLF.
		line, err := reader.R.ReadString('\n')
		if err != nil {
			return err
		}
		if !strings.HasSuffix(line, "\r\n") {
			return textproto.ProtocolError("SMTP DATA reply is missing its terminating CRLF")
		}
		line = strings.TrimSuffix(line, "\r\n")
		if len(line) < 3 || line[0] < '2' || line[0] > '5' ||
			line[1] < '0' || line[1] > '5' || line[2] < '0' || line[2] > '9' ||
			(len(line) > 3 && line[3] != ' ' && line[3] != '-') {
			return textproto.ProtocolError(fmt.Sprintf("invalid SMTP DATA reply: %q", line))
		}
		current := int(line[0]-'0')*100 + int(line[1]-'0')*10 + int(line[2]-'0')
		if code != 0 && code != current {
			return textproto.ProtocolError(fmt.Sprintf("inconsistent SMTP DATA reply codes: %d and %d", code, current))
		}
		code = current
		if message.Len() > 0 {
			message.WriteByte('\n')
		}
		if len(line) > 3 {
			message.WriteString(line[4:])
		}
		if len(line) == 3 || line[3] == ' ' {
			if code == expectedCode {
				return nil
			}
			return &textproto.Error{Code: code, Msg: message.String()}
		}
	}
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
