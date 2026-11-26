package bridge

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/ianaindex"
)

type IMAPConfig struct {
	Host        string
	Port        int
	Username    string
	Password    string
	TLSCertFile string
	TLSPolicy   *TLSPolicy
}

func (cfg IMAPConfig) tlsConfig() (*tls.Config, error) {
	return cfg.TLSPolicy.configFor("IMAP", cfg.Host, cfg.TLSCertFile)
}

type DraftMessage struct {
	UID        string
	Mailbox    string
	From       string
	To         []string
	Subject    string
	Body       string
	Date       time.Time
	Flags      []string
	MessageID  string
	InReplyTo  string
	References string
}

type IMAPClient struct {
	conn         net.Conn
	r            *bufio.Reader
	w            *bufio.Writer
	tag          int
	timeout      time.Duration
	debug        bool
	draftMailbox *DraftMailbox
	tlsPolicy    *TLSPolicy
}

var ErrMessageNotFound = errors.New("message not found")

var ErrAppendRejected = errors.New("IMAP server rejected APPEND")

var ErrAppendUncertain = errors.New("draft append outcome or identity is uncertain; inspect Drafts before retrying")

var (
	literalRe = regexp.MustCompile(`\{(\d+)\}\r?$`)
	uidRe     = regexp.MustCompile(`(?i)UID\s+(\d+)`)
	flagsRe   = regexp.MustCompile(`(?i)FLAGS\s+\(([^)]*)\)`)
	// Flags, delimiter, mailbox name (quoted, literal, or atom), then optional LIST extensions.
	listMailboxRe = regexp.MustCompile(`(?i)^\* LIST \(([^)]*)\) (?:NIL|"(?:\\.|[^"\\])*") (?:"((?:\\["\\]|[^"\\])*)"|\{([0-9]+)\}|([^\s(){"]+))(.*)$`)
)

func DialIMAP(cfg IMAPConfig, timeout time.Duration) (*IMAPClient, error) {
	if err := validateIMAPString(cfg.Username); err != nil {
		return nil, fmt.Errorf("invalid IMAP username: %w", err)
	}
	if err := validateIMAPString(cfg.Password); err != nil {
		return nil, fmt.Errorf("invalid IMAP password: %w", err)
	}
	tlsConfig, err := cfg.tlsConfig()
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c := &IMAPClient{
		conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn),
		tag: 1, timeout: timeout, debug: strings.TrimSpace(os.Getenv("PMAIL_IMAP_DEBUG")) == "1",
		tlsPolicy: &TLSPolicy{config: tlsConfig, certificateFile: cfg.TLSCertFile},
	}
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

	if err := c.startTLS(tlsConfig); err != nil {
		// The TLS upgrade may have failed after the plaintext protocol ended.
		// Close the socket without writing through the pre-TLS buffered writer.
		_ = conn.Close()
		return nil, err
	}
	if err := c.login(cfg.Username, cfg.Password); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *IMAPClient) TLSPolicy() *TLSPolicy {
	return c.tlsPolicy
}

func (c *IMAPClient) Close() error {
	_ = c.conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	_, _ = c.w.WriteString("ZZZZ LOGOUT\r\n")
	_ = c.w.Flush()
	return c.conn.Close()
}

type Mailbox struct {
	Name  string
	Flags []string
}

func (c *IMAPClient) ListMailboxes() ([]Mailbox, error) {
	boxes, err := c.listMailboxRows()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(boxes, func(i, j int) bool { return boxes[i].Name < boxes[j].Name })
	unique := boxes[:0]
	for _, box := range boxes {
		if len(unique) == 0 || unique[len(unique)-1].Name != box.Name {
			unique = append(unique, Mailbox{Name: box.Name})
		}
		merged := &unique[len(unique)-1]
		for _, flag := range box.Flags {
			if !merged.hasFlag(flag) {
				merged.Flags = append(merged.Flags, flag)
			}
		}
	}
	return unique, nil
}

func (m Mailbox) hasFlag(flag string) bool {
	return slices.ContainsFunc(m.Flags, func(f string) bool { return strings.EqualFold(f, flag) })
}

// Keep wire order for special-use selection; consume literal bytes before reading
// the next protocol line. An incomplete listing cannot establish draft identity.
func (c *IMAPClient) listMailboxRows() ([]Mailbox, error) {
	boxes := []Mailbox{}
	err := c.commandLines(`LIST "" "*"`, func(line string) error {
		words := strings.Fields(line)
		if len(words) < 2 || words[0] != "*" || !strings.EqualFold(words[1], "LIST") {
			if len(words) > 1 && words[0] == "*" && literalRe.MatchString(line) {
				return errors.New("unsupported literal in response to LIST")
			}
			return nil
		}
		box, err := c.readListedMailbox(line)
		if err != nil {
			return err
		}
		boxes = append(boxes, box)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return boxes, nil
}

func (c *IMAPClient) readListedMailbox(line string) (Mailbox, error) {
	fields := listMailboxRe.FindStringSubmatch(line)
	if fields == nil {
		return Mailbox{}, errors.New("invalid IMAP LIST mailbox row")
	}
	name := fields[4]
	tail := fields[5]
	if fields[3] != "" {
		size, err := strconv.ParseUint(fields[3], 10, 32)
		if err != nil || tail != "" {
			return Mailbox{}, errors.New("invalid IMAP LIST mailbox literal")
		}
		var literal strings.Builder
		if _, err := io.CopyN(&literal, c.r, int64(size)); err != nil {
			return Mailbox{}, fmt.Errorf("read IMAP LIST mailbox literal: %w", err)
		}
		name = literal.String()
		tail, err = c.readLine()
		if err != nil {
			return Mailbox{}, err
		}
	} else if name == "" {
		name = unescapeIMAPQuoted(fields[2])
	}
	if tail != "" && (!strings.HasPrefix(tail, " (") || !strings.HasSuffix(tail, ")")) {
		return Mailbox{}, errors.New("unsupported IMAP LIST mailbox data")
	}
	if name == "" || !utf8.ValidString(name) || validateIMAPString(name) != nil {
		return Mailbox{}, errors.New("unsupported IMAP LIST mailbox name")
	}
	return Mailbox{Name: name, Flags: strings.Fields(fields[1])}, nil
}

func (c *IMAPClient) ListDrafts() ([]DraftMessage, error) {
	mb, err := c.DraftMailboxName()
	if err != nil {
		return nil, err
	}
	return c.ListMessages(mb, "ALL")
}

func (c *IMAPClient) ListMessages(mailbox, criteria string) ([]DraftMessage, error) {
	if err := validateSearchCriteria(criteria); err != nil {
		return nil, err
	}
	if err := c.examineMailbox(mailbox); err != nil {
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
			return nil, fmt.Errorf("fetch message UID %s from mailbox %q: %w", uid, mailbox, err)
		}
		msgs = append(msgs, m)
	}
	sort.Slice(msgs, func(i, j int) bool { return uidInt(msgs[i].UID) < uidInt(msgs[j].UID) })
	return msgs, nil
}

func (c *IMAPClient) GetDraft(uid string) (DraftMessage, error) {
	if err := validateUID(uid); err != nil {
		return DraftMessage{}, err
	}
	mb, err := c.DraftMailboxName()
	if err != nil {
		return DraftMessage{}, err
	}
	return c.GetMessage(mb, uid)
}

// GetMessage fetches a known UID from its exact mailbox without a SEARCH or
// special-use mailbox lookup.
func (c *IMAPClient) GetMessage(mailbox, uid string) (DraftMessage, error) {
	if err := validateUID(uid); err != nil {
		return DraftMessage{}, err
	}
	if err := c.examineMailbox(mailbox); err != nil {
		return DraftMessage{}, err
	}
	return c.fetchUID(mailbox, uid)
}

func (c *IMAPClient) AppendDraft(raw string) (string, error) {
	mb, err := c.DraftMailboxName()
	if err != nil {
		return "", err
	}
	if err := c.selectMailbox(mb); err != nil {
		return "", err
	}
	tag := c.nextTag()
	cmd := fmt.Sprintf("%s APPEND \"%s\" () {%d}\r\n", tag, escape(mb), len(raw))
	c.debugf("C: %s APPEND \"%s\" () {%d}", tag, mb, len(raw))
	if _, err := c.w.WriteString(cmd); err != nil {
		return "", err
	}
	if err := c.w.Flush(); err != nil {
		return "", err
	}
	line, err := c.readAppendReply(tag)
	if err != nil {
		return "", err
	}
	c.debugf("S: %s", line)
	if !strings.HasPrefix(line, "+") {
		if appendRejected(tag, strings.Fields(line)) {
			return "", fmt.Errorf("%w: %s", ErrAppendRejected, line)
		}
		return "", fmt.Errorf("expected IMAP APPEND continuation: %s", line)
	}

	c.debugf("C: [literal %d bytes]", len(raw))
	if _, err := c.w.WriteString(raw + "\r\n"); err != nil {
		return "", fmt.Errorf("%w: %v", ErrAppendUncertain, err)
	}
	if err := c.w.Flush(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrAppendUncertain, err)
	}
	line, err = c.readAppendReply(tag)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrAppendUncertain, err)
	}
	fields := strings.Fields(line)
	if appendRejected(tag, fields) {
		return "", fmt.Errorf("%w: %s", ErrAppendRejected, line)
	}
	// APPENDUID identifies this append even when another client writes concurrently.
	if len(fields) >= 5 && fields[0] == tag && strings.EqualFold(fields[1], "OK") &&
		strings.EqualFold(fields[2], "[APPENDUID") && strings.HasSuffix(fields[4], "]") {
		validity, validityErr := strconv.ParseUint(fields[3], 10, 32)
		uid, uidErr := strconv.ParseUint(strings.TrimSuffix(fields[4], "]"), 10, 32)
		if validityErr == nil && uidErr == nil && validity > 0 && uid > 0 {
			return strconv.FormatUint(uid, 10), nil
		}
	}
	return "", ErrAppendUncertain
}

// APPEND may receive unsolicited status or mailbox updates before either the
// literal continuation or the tagged completion.
func (c *IMAPClient) readAppendReply(tag string) (string, error) {
	for {
		line, err := c.readLine()
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, tag+" ") {
			return line, nil
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "*" && !strings.EqualFold(fields[1], "BYE") && !literalRe.MatchString(line) {
			continue
		}
		// An unsupported literal may leave unread bytes on the connection.
		if c.conn != nil {
			_ = c.conn.Close()
		}
		return "", fmt.Errorf("unexpected IMAP response during APPEND: %s", line)
	}
}

func appendRejected(tag string, fields []string) bool {
	return len(fields) >= 2 && fields[0] == tag && (strings.EqualFold(fields[1], "NO") || strings.EqualFold(fields[1], "BAD"))
}

func (c *IMAPClient) DeleteDraft(uid string) error {
	if err := validateUID(uid); err != nil {
		return err
	}
	lines, err := c.simpleLines("CAPABILITY")
	if err != nil {
		return err
	}
	scopedExpunge := false
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "*" || !strings.EqualFold(fields[1], "CAPABILITY") {
			continue
		}
		for _, capability := range fields[2:] {
			scopedExpunge = scopedExpunge || strings.EqualFold(capability, "UIDPLUS") || strings.EqualFold(capability, "IMAP4rev2")
		}
	}
	if !scopedExpunge {
		return errors.New("safe draft deletion requires IMAP UIDPLUS or IMAP4rev2 (UID EXPUNGE); no flags were changed")
	}
	mb, err := c.DraftMailboxName()
	if err != nil {
		return err
	}
	if err := c.selectMailbox(mb); err != nil {
		return err
	}
	if err := c.simple(fmt.Sprintf(`UID STORE %s +FLAGS.SILENT (\Deleted)`, uid)); err != nil {
		return err
	}
	return c.simple("UID EXPUNGE " + uid)
}

func (c *IMAPClient) SetKeyword(mailbox, uid, keyword string, add bool) error {
	if err := validateUID(uid); err != nil {
		return err
	}
	if err := ValidateKeyword(keyword); err != nil {
		return err
	}
	if err := c.selectMailbox(mailbox); err != nil {
		return err
	}
	op := "+FLAGS.SILENT"
	if !add {
		op = "-FLAGS.SILENT"
	}
	return c.simple(fmt.Sprintf("UID STORE %s %s (%s)", uid, op, keyword))
}

func (c *IMAPClient) startTLS(config *tls.Config) error {
	if err := c.simple("STARTTLS"); err != nil {
		return err
	}
	tlsConn := tls.Client(c.conn, config)
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
	debug := c.debug
	c.debugf("C: LOGIN [redacted]")
	c.debug = false
	defer func() { c.debug = debug }()
	if err := c.simple(fmt.Sprintf(`LOGIN "%s" "%s"`, escape(user), escape(pass))); err != nil {
		return errors.New("IMAP authentication failed (check Bridge credentials and connectivity)")
	}
	return nil
}

func (c *IMAPClient) examineMailbox(mailbox string) error {
	if err := validateIMAPString(mailbox); err != nil {
		return fmt.Errorf("invalid mailbox: %w", err)
	}
	return c.simple(fmt.Sprintf(`EXAMINE "%s"`, escape(mailbox)))
}

func (c *IMAPClient) selectMailbox(mailbox string) error {
	if err := validateIMAPString(mailbox); err != nil {
		return fmt.Errorf("invalid mailbox: %w", err)
	}
	return c.simple(fmt.Sprintf(`SELECT "%s"`, escape(mailbox)))
}

func (c *IMAPClient) searchUID(criteria string) ([]string, error) {
	if err := validateSearchCriteria(criteria); err != nil {
		return nil, err
	}
	lines, err := c.simpleLines("UID SEARCH " + criteria)
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[0] == "*" && strings.EqualFold(parts[1], "SEARCH") {
			for _, uid := range parts[2:] {
				if err := validateUID(uid); err != nil {
					return nil, fmt.Errorf("invalid UID in SEARCH response: %w", err)
				}
			}
			return parts[2:], nil
		}
	}
	return []string{}, nil
}

func (c *IMAPClient) fetchUID(mailbox, uid string) (DraftMessage, error) {
	if err := validateUID(uid); err != nil {
		return DraftMessage{}, err
	}
	tag := c.nextTag()
	cmd := fmt.Sprintf("%s UID FETCH %s (UID FLAGS BODY.PEEK[])\r\n", tag, uid)
	if _, err := c.w.WriteString(cmd); err != nil {
		return DraftMessage{}, err
	}
	if err := c.w.Flush(); err != nil {
		return DraftMessage{}, err
	}
	var raw []byte
	var flags []string
	fetched := false
	for {
		line, err := c.readLine()
		if err != nil {
			return DraftMessage{}, err
		}
		if strings.HasPrefix(line, "*") && strings.Contains(strings.ToUpper(line), "FETCH") {
			metadata := line
			var messageRaw []byte
			hasLiteral := false
			if lm := literalRe.FindStringSubmatch(line); len(lm) == 2 {
				n, err := strconv.Atoi(lm[1])
				if err != nil {
					return DraftMessage{}, fmt.Errorf("invalid FETCH literal size: %w", err)
				}
				messageRaw = make([]byte, n)
				if _, err := io.ReadFull(c.r, messageRaw); err != nil {
					return DraftMessage{}, err
				}
				suffix, err := c.readLine()
				if err != nil {
					return DraftMessage{}, err
				}
				metadata += " " + suffix
				hasLiteral = true
			}
			// Unsolicited flag updates may concern another UID or omit UID.
			// UID can precede or follow the body literal in a FETCH response.
			um := uidRe.FindStringSubmatch(flagsRe.ReplaceAllString(metadata, ""))
			if len(um) != 2 || um[1] != uid {
				continue
			}
			fetched = true
			if fm := flagsRe.FindStringSubmatch(metadata); len(fm) == 2 {
				flags = strings.Fields(strings.TrimSpace(fm[1]))
			}
			if hasLiteral {
				raw = messageRaw
			}
			continue
		}
		switch taggedStatus(line, tag) {
		case "OK":
			if !fetched {
				return DraftMessage{}, ErrMessageNotFound
			}
			msg, err := parseRawMessage(raw)
			if err != nil {
				return DraftMessage{}, err
			}
			msg.UID = uid
			msg.Mailbox = mailbox
			msg.Flags = flags
			return msg, nil
		case "NO", "BAD":
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
	bodyBytes, err := io.ReadAll(m.Body)
	if err != nil {
		return DraftMessage{}, err
	}
	body, err := decodeBestBody(m.Header, bodyBytes)
	if err != nil {
		return DraftMessage{}, err
	}
	decoder := &mime.WordDecoder{CharsetReader: charsetReader}
	subject, err := decoder.DecodeHeader(m.Header.Get("Subject"))
	if err != nil {
		return DraftMessage{}, fmt.Errorf("decode Subject: %w", err)
	}
	date, _ := mail.ParseDate(m.Header.Get("Date"))
	return DraftMessage{
		From:       m.Header.Get("From"),
		To:         to,
		Subject:    subject,
		Body:       body,
		Date:       date,
		MessageID:  m.Header.Get("Message-ID"),
		InReplyTo:  m.Header.Get("In-Reply-To"),
		References: m.Header.Get("References"),
	}, nil
}

func BuildRawMessage(from string, to []string, subject, body string) string {
	return BuildRawMessageWithHeaders(from, to, subject, body, nil)
}

func BuildRawMessageWithHeaders(from string, to []string, subject, body string, extraHeaders map[string]string) string {
	headers := []string{
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", strings.Join(to, ", ")),
		fmt.Sprintf("Subject: %s", subject),
		fmt.Sprintf("Date: %s", time.Now().UTC().Format(time.RFC1123Z)),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
	}
	if len(extraHeaders) > 0 {
		keys := make([]string, 0, len(extraHeaders))
		for k := range extraHeaders {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			headers = append(headers, fmt.Sprintf("%s: %s", k, extraHeaders[k]))
		}
	}
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body
}

func (c *IMAPClient) simple(cmd string) error {
	_, err := c.simpleLines(cmd)
	return err
}

func (c *IMAPClient) simpleLines(cmd string) ([]string, error) {
	var lines []string
	err := c.commandLines(cmd, func(line string) error {
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lines, nil
}

func (c *IMAPClient) commandLines(cmd string, visit func(string) error) error {
	tag := c.nextTag()
	if _, err := fmt.Fprintf(c.w, "%s %s\r\n", tag, cmd); err != nil {
		return err
	}
	if err := c.w.Flush(); err != nil {
		return err
	}
	c.debugf("C: %s %s", tag, cmd)
	for {
		line, err := c.readLine()
		if err != nil {
			return err
		}
		if err := visit(line); err != nil {
			// A failed decoder can leave unread literal bytes and a tagged reply.
			if c.conn != nil {
				_ = c.conn.Close()
			}
			return err
		}
		switch taggedStatus(line, tag) {
		case "OK":
			return nil
		case "NO", "BAD":
			return fmt.Errorf("imap command failed: %s", line)
		}
	}
}

func taggedStatus(line, tag string) string {
	replyTag, response, tagged := strings.Cut(line, " ")
	if !tagged || replyTag != tag {
		return ""
	}
	status, _, _ := strings.Cut(response, " ")
	return strings.ToUpper(status)
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
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

func (c *IMAPClient) debugf(format string, args ...interface{}) {
	if !c.debug {
		return
	}
	fmt.Fprintf(os.Stderr, "imap-debug: "+format+"\n", args...)
}

// DraftMailbox retains the exact server names that a legacy imap:Drafts ID could address.
type DraftMailbox struct {
	Name              string
	AmbiguousLegacyID bool
	LegacyCandidates  []string
}

// Pin discovery to this connection so validation, reads, writes, and receipts
// use the same mailbox even if another client changes its special-use flags.
func (c *IMAPClient) ResolveDraftMailbox() (DraftMailbox, error) {
	if c.draftMailbox != nil {
		return *c.draftMailbox, nil
	}
	boxes, err := c.listMailboxRows()
	if err != nil {
		return DraftMailbox{}, err
	}
	// A later row can identify the same exact name as nonselectable.
	unselectable := make(map[string]bool)
	for _, box := range boxes {
		if box.hasFlag(`\Noselect`) {
			unselectable[box.Name] = true
		}
	}
	var selected string
	var candidates []string
	roleAdvertised := false
	for _, box := range boxes {
		draftsRole := box.hasFlag(`\Drafts`)
		roleAdvertised = roleAdvertised || draftsRole
		if draftsRole && selected == "" && !unselectable[box.Name] {
			selected = box.Name
		}
		if draftsRole || strings.EqualFold(box.Name, "Drafts") {
			candidates = append(candidates, box.Name)
		}
	}
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)
	if selected == "" {
		if roleAdvertised {
			return DraftMailbox{LegacyCandidates: candidates, AmbiguousLegacyID: len(candidates) > 1}, errors.New("no selectable special-use Drafts mailbox in IMAP listing")
		}
		for _, name := range candidates {
			if unselectable[name] {
				continue
			}
			if selected != "" {
				return DraftMailbox{LegacyCandidates: candidates, AmbiguousLegacyID: true}, fmt.Errorf("ambiguous Drafts mailbox: no special-use role distinguishes %q", candidates)
			}
			selected = name
		}
		if selected == "" {
			return DraftMailbox{}, errors.New("no selectable Drafts mailbox in IMAP listing")
		}
	}
	c.draftMailbox = &DraftMailbox{Name: selected, LegacyCandidates: candidates, AmbiguousLegacyID: len(candidates) > 1}
	return *c.draftMailbox, nil
}

func (c *IMAPClient) DraftMailboxName() (string, error) {
	box, err := c.ResolveDraftMailbox()
	return box.Name, err
}

// ExamineDraftDestination verifies that the resolved mailbox can be opened
// without changing message state or appending a draft.
func (c *IMAPClient) ExamineDraftDestination() error {
	name, err := c.DraftMailboxName()
	if err != nil {
		return err
	}
	return c.examineMailbox(name)
}

func (c *IMAPClient) SearchUIDs(mailbox, criteria string) ([]string, error) {
	if err := validateSearchCriteria(criteria); err != nil {
		return nil, err
	}
	if err := c.examineMailbox(mailbox); err != nil {
		return nil, err
	}
	return c.searchUID(criteria)
}

func (c *IMAPClient) MoveUID(srcMailbox, uid, dstMailbox string) error {
	if err := validateUID(uid); err != nil {
		return err
	}
	if err := validateIMAPString(dstMailbox); err != nil {
		return fmt.Errorf("invalid destination mailbox: %w", err)
	}
	if err := c.selectMailbox(srcMailbox); err != nil {
		return err
	}
	return c.simple(fmt.Sprintf(`UID MOVE %s "%s"`, uid, escape(dstMailbox)))
}

func uidInt(uid string) int {
	n, err := strconv.Atoi(strings.TrimSpace(uid))
	if err != nil {
		return 0
	}
	return n
}

func decodeBestBody(h mail.Header, body []byte) (string, error) {
	text, _, err := decodeBodyPart(h, body)
	return text, err
}

// A plain inline body outranks HTML; attachments (including multipart
// attachments) never participate in choosing the message body.
func decodeBodyPart(h mail.Header, body []byte) (string, int, error) {
	if disposition := h.Get("Content-Disposition"); disposition != "" {
		kind, params, err := mime.ParseMediaType(disposition)
		if err != nil {
			return "", 0, fmt.Errorf("parse Content-Disposition: %w", err)
		}
		if strings.EqualFold(kind, "attachment") || params["filename"] != "" {
			return "", 0, nil
		}
	}
	mediaType := "text/plain"
	params := map[string]string{}
	if contentType := h.Get("Content-Type"); contentType != "" {
		var err error
		mediaType, params, err = mime.ParseMediaType(contentType)
		if err != nil {
			return "", 0, fmt.Errorf("parse Content-Type: %w", err)
		}
	}
	if params["name"] != "" {
		return "", 0, nil
	}
	decoded := decodeByTransferEncoding(h.Get("Content-Transfer-Encoding"), body)
	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", 0, errors.New("missing multipart boundary")
		}
		reader := multipart.NewReader(bytes.NewReader(decoded), boundary)
		var best string
		var firstDecodeError error
		bestRank := 0
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", 0, fmt.Errorf("read MIME part: %w", err)
			}
			partBody, err := io.ReadAll(part)
			if err != nil {
				return "", 0, fmt.Errorf("read MIME body: %w", err)
			}
			text, rank, err := decodeBodyPart(mail.Header(part.Header), partBody)
			if err != nil {
				if firstDecodeError == nil {
					firstDecodeError = err
				}
				continue
			}
			if rank > bestRank {
				best, bestRank = text, rank
			}
		}
		if bestRank == 0 && firstDecodeError != nil {
			return "", 0, firstDecodeError
		}
		return best, bestRank, nil
	}
	rank := 0
	switch strings.ToLower(mediaType) {
	case "text/plain":
		rank = 2
	case "text/html":
		rank = 1
	default:
		return "", 0, nil
	}
	text, err := decodeTextCharset(params["charset"], decoded)
	return text, rank, err
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

func validateUID(uid string) error {
	if len(uid) == 0 || uid[0] < '1' || uid[0] > '9' {
		return fmt.Errorf("invalid UID %q: expected one positive 32-bit integer", uid)
	}
	for _, ch := range uid {
		if ch < '0' || ch > '9' {
			return fmt.Errorf("invalid UID %q: expected one positive 32-bit integer", uid)
		}
	}
	if _, err := strconv.ParseUint(uid, 10, 32); err != nil {
		return fmt.Errorf("invalid UID %q: expected one positive 32-bit integer", uid)
	}
	return nil
}

func validateIMAPString(value string) error {
	for _, ch := range value {
		if ch < 32 || ch == 127 {
			return errors.New("control characters are not allowed")
		}
	}
	return nil
}

func validateIMAPAtom(value string) error {
	if value == "" {
		return errors.New("empty atom")
	}
	for _, ch := range value {
		if ch <= 32 || ch >= 127 || strings.ContainsRune(`(){%*"\]`, ch) {
			return errors.New("expected an IMAP atom")
		}
	}
	return nil
}

// Search criteria are generated by the app. Accept that grammar explicitly so
// quoted search text cannot escape into another key or a protocol literal.
func validateSearchCriteria(criteria string) error {
	if err := validateIMAPString(criteria); err != nil {
		return fmt.Errorf("invalid search criteria: %w", err)
	}
	type token struct {
		text   string
		quoted bool
	}
	var tokens []token
	for i := 0; i < len(criteria); {
		if criteria[i] == ' ' {
			i++
			continue
		}
		if criteria[i] != '"' {
			start := i
			for i < len(criteria) && criteria[i] != ' ' {
				if strings.ContainsRune(`"\(){`, rune(criteria[i])) {
					return errors.New("invalid search token")
				}
				i++
			}
			tokens = append(tokens, token{text: criteria[start:i]})
			continue
		}
		i++
		var value strings.Builder
		closed := false
		for i < len(criteria) {
			ch := criteria[i]
			i++
			if ch == '"' {
				closed = true
				break
			}
			if ch == '\\' {
				if i == len(criteria) || (criteria[i] != '\\' && criteria[i] != '"') {
					return errors.New("invalid quoted search escape")
				}
				ch = criteria[i]
				i++
			}
			value.WriteByte(ch)
		}
		if !closed || (i < len(criteria) && criteria[i] != ' ') {
			return errors.New("invalid quoted search string")
		}
		tokens = append(tokens, token{text: value.String(), quoted: true})
	}
	if len(tokens) == 0 {
		return errors.New("empty search criteria")
	}
	for i := 0; i < len(tokens); i++ {
		key := tokens[i]
		if key.quoted {
			return errors.New("invalid search key")
		}
		switch strings.ToUpper(key.text) {
		case "ALL", "UNSEEN":
		case "KEYWORD":
			i++
			if i >= len(tokens) || tokens[i].quoted {
				return errors.New("search keyword must be an atom")
			}
			if err := ValidateKeyword(tokens[i].text); err != nil {
				return err
			}
		case "TEXT", "SUBJECT", "FROM", "TO":
			i++
			if i >= len(tokens) || !tokens[i].quoted {
				return errors.New("search text must be quoted")
			}
		case "HEADER":
			i++
			if i >= len(tokens) || tokens[i].quoted || validateIMAPAtom(tokens[i].text) != nil {
				return errors.New("invalid search header name")
			}
			i++
			if i >= len(tokens) || !tokens[i].quoted {
				return errors.New("search header text must be quoted")
			}
		case "UID":
			i++
			if i >= len(tokens) || tokens[i].quoted {
				return errors.New("invalid UID search")
			}
			uid := strings.TrimSuffix(tokens[i].text, ":*")
			if err := validateUID(uid); err != nil {
				return err
			}
		case "SINCE", "BEFORE":
			i++
			if i >= len(tokens) || tokens[i].quoted {
				return errors.New("invalid search date")
			}
			if _, err := time.Parse("02-Jan-2006", tokens[i].text); err != nil {
				return errors.New("invalid search date")
			}
		default:
			return fmt.Errorf("unsupported search key %q", key.text)
		}
	}
	return nil
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	encoding, err := ianaindex.MIME.Encoding(charset)
	if err != nil || encoding == nil {
		return nil, fmt.Errorf("unsupported MIME charset %q", charset)
	}
	return encoding.NewDecoder().Reader(input), nil
}

func decodeTextCharset(charset string, body []byte) (string, error) {
	if charset == "" {
		return string(body), nil
	}
	reader, err := charsetReader(charset, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("decode MIME charset %q: %w", charset, err)
	}
	return string(decoded), nil
}

func unescapeIMAPQuoted(value string) string {
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(value)
}

// ValidateKeyword accepts a user-defined IMAP flag atom, excluding system flags.
func ValidateKeyword(keyword string) error {
	if err := validateIMAPAtom(keyword); err != nil {
		return fmt.Errorf("invalid IMAP keyword: %w", err)
	}
	return nil
}
