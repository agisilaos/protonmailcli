package bridge

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type IMAPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
}

type DraftMessage struct {
	UID     string
	Mailbox string
	From    string
	To      []string
	Subject string
	Body    string
	Date    time.Time
	Flags   []string
}

type IMAPClient struct {
	conn    net.Conn
	r       *bufio.Reader
	w       *bufio.Writer
	tag     int
	timeout time.Duration
	debug   bool
}

var (
	literalRe = regexp.MustCompile(`\{(\d+)\}\r?$`)
	uidRe     = regexp.MustCompile(`UID\s+(\d+)`)
	flagsRe   = regexp.MustCompile(`FLAGS\s+\(([^)]*)\)`)
	nameRe    = regexp.MustCompile(`"([^"]+)"\s*$`)
)

func DialIMAP(cfg IMAPConfig, timeout time.Duration) (*IMAPClient, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c := &IMAPClient{conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn), tag: 1, timeout: timeout, debug: strings.TrimSpace(os.Getenv("PMAIL_IMAP_DEBUG")) == "1"}
	if err := c.conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = c.Close()
		return nil, err
	}
	greet, err := c.readLine()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	if !strings.HasPrefix(greet, "*") {
		_ = c.Close()
		return nil, fmt.Errorf("invalid IMAP greeting")
	}

	if err := c.startTLS(cfg.Host); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err := c.login(cfg.Username, cfg.Password); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *IMAPClient) Close() error {
	_ = c.simple("LOGOUT")
	return c.conn.Close()
}

func (c *IMAPClient) ListMailboxes() ([]string, error) {
	lines, err := c.simpleLines(`LIST "" "*"`)
	if err != nil {
		return nil, err
	}
	boxes := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.HasPrefix(line, "* LIST") {
			continue
		}
		m := nameRe.FindStringSubmatch(line)
		if len(m) == 2 {
			boxes = append(boxes, m[1])
		}
	}
	sort.Strings(boxes)
	return boxes, nil
}

func (c *IMAPClient) ListDrafts() ([]DraftMessage, error) {
	return c.ListMessages("Drafts", "ALL")
}

func (c *IMAPClient) ListMessages(mailbox, criteria string) ([]DraftMessage, error) {
	if err := c.selectMailbox(mailbox); err != nil {
		return nil, err
	}
	uids, err := c.searchUID(criteria)
	if err != nil {
		return nil, err
	}
	msgs := make([]DraftMessage, 0, len(uids))
	for _, uid := range uids {
		m, err := c.fetchUID(mailbox, uid)
		if err != nil {
			continue
		}
		msgs = append(msgs, m)
	}
	sort.Slice(msgs, func(i, j int) bool { return uidInt(msgs[i].UID) < uidInt(msgs[j].UID) })
	return msgs, nil
}

func (c *IMAPClient) GetDraft(uid string) (DraftMessage, error) {
	if err := c.selectMailbox("Drafts"); err != nil {
		return DraftMessage{}, err
	}
	return c.fetchUID("Drafts", uid)
}

func (c *IMAPClient) AppendDraft(raw string) (string, error) {
	if err := c.selectMailbox("Drafts"); err != nil {
		return "", err
	}
	tag := c.nextTag()
	cmd := fmt.Sprintf(`%s APPEND "Drafts" (\\Draft) {%d}\r\n`, tag, len(raw))
	if _, err := c.w.WriteString(cmd); err != nil {
		return "", err
	}
	if err := c.w.Flush(); err != nil {
		return "", err
	}
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(line, "+") {
		return "", fmt.Errorf("imap append rejected: %s", line)
	}
	if _, err := c.w.WriteString(raw + "\r\n"); err != nil {
		return "", err
	}
	if err := c.w.Flush(); err != nil {
		return "", err
	}
	for {
		line, err := c.readLine()
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(line, tag+" OK") {
			if err := c.selectMailbox("Drafts"); err != nil {
				return "", err
			}
			uids, err := c.searchUID("ALL")
			if err != nil || len(uids) == 0 {
				return "", err
			}
			return uids[len(uids)-1], nil
		}
		if strings.HasPrefix(line, tag+" NO") || strings.HasPrefix(line, tag+" BAD") {
			return "", fmt.Errorf("imap append failed: %s", line)
		}
	}
}

func (c *IMAPClient) DeleteDraft(uid string) error {
	if err := c.selectMailbox("Drafts"); err != nil {
		return err
	}
	if err := c.simple(fmt.Sprintf("UID STORE %s +FLAGS.SILENT (\\\\Deleted)", uid)); err != nil {
		return err
	}
	return c.simple("EXPUNGE")
}

func (c *IMAPClient) SetKeyword(mailbox, uid, keyword string, add bool) error {
	if err := c.selectMailbox(mailbox); err != nil {
		return err
	}
	op := "+FLAGS.SILENT"
	if !add {
		op = "-FLAGS.SILENT"
	}
	return c.simple(fmt.Sprintf("UID STORE %s %s (%s)", uid, op, keyword))
}

func (c *IMAPClient) startTLS(serverName string) error {
	if err := c.simple("STARTTLS"); err != nil {
		return err
	}
	tlsConn := tls.Client(c.conn, &tls.Config{ServerName: serverName, InsecureSkipVerify: true})
	if err := tlsConn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return err
	}
	if err := tlsConn.Handshake(); err != nil {
		return err
	}
	c.conn = tlsConn
	c.r = bufio.NewReader(tlsConn)
	c.w = bufio.NewWriter(tlsConn)
	return nil
}

func (c *IMAPClient) login(user, pass string) error {
	if user == "" || pass == "" {
		return fmt.Errorf("missing IMAP credentials")
	}
	return c.simple(fmt.Sprintf(`LOGIN "%s" "%s"`, escape(user), escape(pass)))
}

func (c *IMAPClient) selectMailbox(mailbox string) error {
	return c.simple(fmt.Sprintf(`SELECT "%s"`, escape(mailbox)))
}

func (c *IMAPClient) searchUID(criteria string) ([]string, error) {
	lines, err := c.simpleLines("UID SEARCH " + criteria)
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "* SEARCH") {
			parts := strings.Fields(line)
			if len(parts) <= 2 {
				return []string{}, nil
			}
			return parts[2:], nil
		}
	}
	return []string{}, nil
}

func (c *IMAPClient) fetchUID(mailbox, uid string) (DraftMessage, error) {
	tag := c.nextTag()
	cmd := fmt.Sprintf("%s UID FETCH %s (UID FLAGS RFC822)\r\n", tag, uid)
	if _, err := c.w.WriteString(cmd); err != nil {
		return DraftMessage{}, err
	}
	if err := c.w.Flush(); err != nil {
		return DraftMessage{}, err
	}
	var raw []byte
	var flags []string
	for {
		line, err := c.readLine()
		if err != nil {
			return DraftMessage{}, err
		}
		if strings.HasPrefix(line, "*") && strings.Contains(line, "FETCH") {
			if fm := flagsRe.FindStringSubmatch(line); len(fm) == 2 {
				flags = strings.Fields(strings.TrimSpace(fm[1]))
			}
			if lm := literalRe.FindStringSubmatch(line); len(lm) == 2 {
				n, _ := strconv.Atoi(lm[1])
				buf := make([]byte, n)
				if _, err := io.ReadFull(c.r, buf); err != nil {
					return DraftMessage{}, err
				}
				raw = buf
				_, _ = c.readLine()
			}
			continue
		}
		if strings.HasPrefix(line, tag+" OK") {
			msg, err := parseRawMessage(raw)
			if err != nil {
				return DraftMessage{}, err
			}
			msg.UID = uid
			msg.Mailbox = mailbox
			msg.Flags = flags
			return msg, nil
		}
		if strings.HasPrefix(line, tag+" NO") || strings.HasPrefix(line, tag+" BAD") {
			return DraftMessage{}, fmt.Errorf("imap fetch failed: %s", line)
		}
	}
}

func parseRawMessage(raw []byte) (DraftMessage, error) {
	if len(raw) == 0 {
		return DraftMessage{}, fmt.Errorf("empty message")
	}
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return DraftMessage{}, err
	}
	to := []string{}
	if v := m.Header.Get("To"); v != "" {
		if list, err := mail.ParseAddressList(v); err == nil {
			for _, a := range list {
				to = append(to, a.Address)
			}
		}
	}
	bodyBytes, _ := io.ReadAll(m.Body)
	body := decodeBestBody(m.Header, bodyBytes)
	date, _ := mail.ParseDate(m.Header.Get("Date"))
	return DraftMessage{
		From:    m.Header.Get("From"),
		To:      to,
		Subject: m.Header.Get("Subject"),
		Body:    body,
		Date:    date,
	}, nil
}

func BuildRawMessage(from string, to []string, subject, body string) string {
	headers := []string{
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", strings.Join(to, ", ")),
		fmt.Sprintf("Subject: %s", subject),
		fmt.Sprintf("Date: %s", time.Now().UTC().Format(time.RFC1123Z)),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
	}
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body
}

func (c *IMAPClient) simple(cmd string) error {
	_, err := c.simpleLines(cmd)
	return err
}

func (c *IMAPClient) simpleLines(cmd string) ([]string, error) {
	tag := c.nextTag()
	if _, err := c.w.WriteString(fmt.Sprintf("%s %s\r\n", tag, cmd)); err != nil {
		return nil, err
	}
	if err := c.w.Flush(); err != nil {
		return nil, err
	}
	c.debugf("C: %s %s", tag, cmd)
	lines := []string{}
	for {
		line, err := c.readLine()
		if err != nil {
			return nil, err
		}
		c.debugf("S: %s", line)
		lines = append(lines, line)
		if strings.HasPrefix(line, tag+" OK") {
			return lines, nil
		}
		if strings.HasPrefix(line, tag+" NO") || strings.HasPrefix(line, tag+" BAD") {
			return nil, fmt.Errorf("imap command failed: %s", line)
		}
	}
}

func (c *IMAPClient) nextTag() string {
	t := fmt.Sprintf("A%04d", c.tag)
	c.tag++
	return t
}

func (c *IMAPClient) readLine() (string, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	out := strings.TrimRight(line, "\r\n")
	c.debugf("S: %s", out)
	return out, nil
}

func escape(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

func (c *IMAPClient) debugf(format string, args ...interface{}) {
	if !c.debug {
		return
	}
	fmt.Fprintf(os.Stderr, "imap-debug: "+format+"\n", args...)
}

func uidInt(uid string) int {
	n, err := strconv.Atoi(strings.TrimSpace(uid))
	if err != nil {
		return 0
	}
	return n
}

func decodeBestBody(h mail.Header, body []byte) string {
	decoded := decodeByTransferEncoding(h.Get("Content-Transfer-Encoding"), body)
	ct := h.Get("Content-Type")
	if ct == "" {
		return strings.TrimSpace(string(decoded))
	}
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return strings.TrimSpace(string(decoded))
	}
	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return strings.TrimSpace(string(decoded))
		}
		return extractMultipartBody(boundary, decoded)
	}
	return strings.TrimSpace(string(decoded))
}

func extractMultipartBody(boundary string, raw []byte) string {
	r := multipart.NewReader(bytes.NewReader(raw), boundary)
	var htmlFallback string
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		pb, _ := io.ReadAll(p)
		sub := decodeByTransferEncoding(p.Header.Get("Content-Transfer-Encoding"), pb)
		ct := p.Header.Get("Content-Type")
		mt, sp, err := mime.ParseMediaType(ct)
		if err != nil {
			mt = "text/plain"
		}
		if strings.HasPrefix(strings.ToLower(mt), "multipart/") {
			if b := sp["boundary"]; b != "" {
				nested := extractMultipartBody(b, sub)
				if nested != "" {
					return nested
				}
			}
			continue
		}
		if strings.EqualFold(mt, "text/plain") {
			return strings.TrimSpace(string(sub))
		}
		if strings.EqualFold(mt, "text/html") && htmlFallback == "" {
			htmlFallback = strings.TrimSpace(string(sub))
		}
	}
	return htmlFallback
}

func decodeByTransferEncoding(encoding string, data []byte) []byte {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		r := quotedprintable.NewReader(bytes.NewReader(data))
		out, err := io.ReadAll(r)
		if err == nil {
			return out
		}
	case "base64":
		out, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(data)))
		if err == nil {
			return out
		}
	}
	return data
}
