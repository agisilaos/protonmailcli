package app

import (
	"flag"
	"io"
)

type setupFlags struct {
	interactive    bool
	nonInteractive bool
	host           string
	smtpPort       int
	imapPort       int
	username       string
	passwordFile   string
	profile        string
}

func newSetupFlags() (*flag.FlagSet, *setupFlags) {
	opts := &setupFlags{}
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.interactive, "interactive", false, "interactive prompts")
	fs.BoolVar(&opts.nonInteractive, "non-interactive", false, "disable prompts")
	fs.StringVar(&opts.host, "bridge-host", "127.0.0.1", "Bridge host")
	fs.IntVar(&opts.smtpPort, "bridge-smtp-port", 1025, "Bridge SMTP port")
	fs.IntVar(&opts.imapPort, "bridge-imap-port", 1143, "Bridge IMAP port")
	fs.StringVar(&opts.username, "username", "", "Bridge username/email")
	fs.StringVar(&opts.passwordFile, "smtp-password-file", "", "path to Bridge SMTP password file")
	fs.StringVar(&opts.profile, "profile", "default", "Profile name")
	return fs, opts
}

type imapMessageFollowUpFlags struct {
	msgID          string
	to             sliceFlag
	subject        string
	body           string
	bodyFile       string
	stdinBody      bool
	idempotencyKey string
}

func newIMAPMessageFollowUpFlags() (*flag.FlagSet, *imapMessageFollowUpFlags) {
	opts := &imapMessageFollowUpFlags{}
	fs := flag.NewFlagSet("message follow-up", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.msgID, "message-id", "", "message id")
	fs.StringVar(&opts.subject, "subject", "", "subject override")
	fs.StringVar(&opts.body, "body", "", "body")
	fs.StringVar(&opts.bodyFile, "body-file", "", "body from file or -")
	fs.BoolVar(&opts.stdinBody, "stdin", false, "read body from stdin")
	fs.StringVar(&opts.idempotencyKey, "idempotency-key", "", "idempotency key")
	fs.Var(&opts.to, "to", "recipient (repeat)")
	return fs, opts
}

type imapMessageSendManyFlags struct {
	file           string
	fromStdin      bool
	passwordFile   string
	idempotencyKey string
}

func newIMAPMessageSendManyFlags() (*flag.FlagSet, *imapMessageSendManyFlags) {
	opts := &imapMessageSendManyFlags{}
	fs := flag.NewFlagSet("message send-many", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.file, "file", "", "manifest json path or -")
	fs.BoolVar(&opts.fromStdin, "stdin", false, "read manifest json from stdin")
	fs.StringVar(&opts.passwordFile, "smtp-password-file", "", "path to smtp password file")
	fs.StringVar(&opts.idempotencyKey, "idempotency-key", "", "idempotency key")
	return fs, opts
}

type imapMessageSendFlags struct {
	draftID        string
	confirm        string
	force          bool
	passwordFile   string
	idempotencyKey string
}

func newIMAPMessageSendFlags() (*flag.FlagSet, *imapMessageSendFlags) {
	opts := &imapMessageSendFlags{}
	fs := flag.NewFlagSet("message send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.draftID, "draft-id", "", "draft id")
	fs.StringVar(&opts.confirm, "confirm-send", "", "confirmation token")
	fs.BoolVar(&opts.force, "force", false, "force send without confirm token")
	fs.StringVar(&opts.passwordFile, "smtp-password-file", "", "path to smtp password file")
	fs.StringVar(&opts.idempotencyKey, "idempotency-key", "", "idempotency key")
	return fs, opts
}

type imapMessageGetFlags struct {
	id string
}

func newIMAPMessageGetFlags() (*flag.FlagSet, *imapMessageGetFlags) {
	opts := &imapMessageGetFlags{}
	fs := flag.NewFlagSet("message get", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.id, "message-id", "", "message id")
	return fs, opts
}

type imapDraftDeleteFlags struct {
	id string
}

func newIMAPDraftDeleteFlags() (*flag.FlagSet, *imapDraftDeleteFlags) {
	opts := &imapDraftDeleteFlags{}
	fs := flag.NewFlagSet("draft delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.id, "draft-id", "", "draft id")
	return fs, opts
}

type imapDraftUpdateFlags struct {
	id        string
	subject   string
	body      string
	bodyFile  string
	stdinBody bool
}

func newIMAPDraftUpdateFlags() (*flag.FlagSet, *imapDraftUpdateFlags) {
	opts := &imapDraftUpdateFlags{}
	fs := flag.NewFlagSet("draft update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.id, "draft-id", "", "draft id")
	fs.StringVar(&opts.subject, "subject", "", "subject")
	fs.StringVar(&opts.body, "body", "", "body")
	fs.StringVar(&opts.bodyFile, "body-file", "", "body from file or -")
	fs.BoolVar(&opts.stdinBody, "stdin", false, "read body from stdin")
	return fs, opts
}

type imapDraftCreateManyFlags struct {
	file           string
	fromStdin      bool
	idempotencyKey string
}

func newIMAPDraftCreateManyFlags() (*flag.FlagSet, *imapDraftCreateManyFlags) {
	opts := &imapDraftCreateManyFlags{}
	fs := flag.NewFlagSet("draft create-many", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.file, "file", "", "manifest json path or -")
	fs.BoolVar(&opts.fromStdin, "stdin", false, "read manifest json from stdin")
	fs.StringVar(&opts.idempotencyKey, "idempotency-key", "", "idempotency key")
	return fs, opts
}

type imapDraftCreateFlags struct {
	to             sliceFlag
	subject        string
	body           string
	bodyFile       string
	stdinBody      bool
	idempotencyKey string
}

func newIMAPDraftCreateFlags() (*flag.FlagSet, *imapDraftCreateFlags) {
	opts := &imapDraftCreateFlags{}
	fs := flag.NewFlagSet("draft create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.subject, "subject", "", "subject")
	fs.StringVar(&opts.body, "body", "", "body")
	fs.StringVar(&opts.bodyFile, "body-file", "", "body from file or -")
	fs.BoolVar(&opts.stdinBody, "stdin", false, "read body from stdin")
	fs.StringVar(&opts.idempotencyKey, "idempotency-key", "", "idempotency key")
	fs.Var(&opts.to, "to", "recipient (repeat)")
	return fs, opts
}

type imapDraftGetFlags struct {
	id string
}

func newIMAPDraftGetFlags() (*flag.FlagSet, *imapDraftGetFlags) {
	opts := &imapDraftGetFlags{}
	fs := flag.NewFlagSet("draft get", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.id, "draft-id", "", "draft id")
	return fs, opts
}

type imapDraftListFlags struct {
	query  string
	from   string
	to     string
	after  string
	before string
	limit  int
	cursor string
}

func newIMAPDraftListFlags() (*flag.FlagSet, *imapDraftListFlags) {
	opts := &imapDraftListFlags{}
	fs := flag.NewFlagSet("draft list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.query, "query", "", "text query")
	fs.StringVar(&opts.from, "from", "", "from filter")
	fs.StringVar(&opts.to, "to", "", "to filter")
	fs.StringVar(&opts.after, "after", "", "date filter YYYY-MM-DD")
	fs.StringVar(&opts.before, "before", "", "date filter YYYY-MM-DD")
	fs.IntVar(&opts.limit, "limit", 50, "max results")
	fs.StringVar(&opts.cursor, "cursor", "", "offset cursor")
	return fs, opts
}

type localMessageSendManyFlags struct {
	file      string
	fromStdin bool
}

func newLocalMessageSendManyFlags() (*flag.FlagSet, *localMessageSendManyFlags) {
	opts := &localMessageSendManyFlags{}
	fs := flag.NewFlagSet("message send-many", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.file, "file", "", "manifest json path or -")
	fs.BoolVar(&opts.fromStdin, "stdin", false, "read manifest json from stdin")
	return fs, opts
}

type localMessageSendFlags struct {
	draftID      string
	confirm      string
	force        bool
	passwordFile string
}

func newLocalMessageSendFlags() (*flag.FlagSet, *localMessageSendFlags) {
	opts := &localMessageSendFlags{}
	fs := flag.NewFlagSet("message send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.draftID, "draft-id", "", "draft id")
	fs.StringVar(&opts.confirm, "confirm-send", "", "confirmation token")
	fs.BoolVar(&opts.force, "force", false, "force send without confirm token")
	fs.StringVar(&opts.passwordFile, "smtp-password-file", "", "path to smtp password file")
	return fs, opts
}

type localDraftCreateManyFlags struct {
	file      string
	fromStdin bool
}

func newLocalDraftCreateManyFlags() (*flag.FlagSet, *localDraftCreateManyFlags) {
	opts := &localDraftCreateManyFlags{}
	fs := flag.NewFlagSet("draft create-many", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.file, "file", "", "manifest json path or -")
	fs.BoolVar(&opts.fromStdin, "stdin", false, "read manifest json from stdin")
	return fs, opts
}

type localDraftCreateFlags struct {
	to        sliceFlag
	tags      sliceFlag
	subject   string
	body      string
	bodyFile  string
	stdinBody bool
}

func newLocalDraftCreateFlags() (*flag.FlagSet, *localDraftCreateFlags) {
	opts := &localDraftCreateFlags{}
	fs := flag.NewFlagSet("draft create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.subject, "subject", "", "subject")
	fs.StringVar(&opts.body, "body", "", "body")
	fs.StringVar(&opts.bodyFile, "body-file", "", "body from file or -")
	fs.BoolVar(&opts.stdinBody, "stdin", false, "read body from stdin")
	fs.Var(&opts.to, "to", "recipient (repeat)")
	fs.Var(&opts.tags, "tag", "tag (repeat)")
	return fs, opts
}

type imapTagAddRemoveFlags struct {
	msgID string
	tag   string
}

func newIMAPTagAddRemoveFlags() (*flag.FlagSet, *imapTagAddRemoveFlags) {
	opts := &imapTagAddRemoveFlags{}
	fs := flag.NewFlagSet("tag add/remove", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.msgID, "message-id", "", "message id")
	fs.StringVar(&opts.tag, "tag", "", "tag name")
	return fs, opts
}

type imapTagCreateFlags struct {
	name string
}

func newIMAPTagCreateFlags() (*flag.FlagSet, *imapTagCreateFlags) {
	opts := &imapTagCreateFlags{}
	fs := flag.NewFlagSet("tag create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.name, "name", "", "tag name")
	return fs, opts
}

type imapSearchFlags struct {
	query   string
	mailbox string
	from    string
	to      string
	subject string
	hasTag  string
	unread  bool
	sinceID string
	after   string
	before  string
	limit   int
	cursor  string
}

func newIMAPSearchFlags() (*flag.FlagSet, *imapSearchFlags) {
	opts := &imapSearchFlags{}
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.query, "query", "", "query")
	fs.StringVar(&opts.mailbox, "mailbox", "", "mailbox name (messages only)")
	fs.StringVar(&opts.from, "from", "", "from filter")
	fs.StringVar(&opts.to, "to", "", "to filter")
	fs.StringVar(&opts.subject, "subject", "", "subject filter")
	fs.StringVar(&opts.hasTag, "has-tag", "", "imap keyword/tag")
	fs.BoolVar(&opts.unread, "unread", false, "only unread messages")
	fs.StringVar(&opts.sinceID, "since-id", "", "minimum UID (inclusive)")
	fs.StringVar(&opts.after, "after", "", "date filter YYYY-MM-DD")
	fs.StringVar(&opts.before, "before", "", "date filter YYYY-MM-DD")
	fs.IntVar(&opts.limit, "limit", 50, "max results")
	fs.StringVar(&opts.cursor, "cursor", "", "offset cursor")
	return fs, opts
}

type localFilterTestApplyFlags struct {
	id string
}

func newLocalFilterTestApplyFlags() (*flag.FlagSet, *localFilterTestApplyFlags) {
	opts := &localFilterTestApplyFlags{}
	fs := flag.NewFlagSet("filter test/apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.id, "filter-id", "", "filter id")
	return fs, opts
}

type localFilterDeleteFlags struct {
	id string
}

func newLocalFilterDeleteFlags() (*flag.FlagSet, *localFilterDeleteFlags) {
	opts := &localFilterDeleteFlags{}
	fs := flag.NewFlagSet("filter delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.id, "filter-id", "", "filter id")
	return fs, opts
}

type localFilterCreateFlags struct {
	name      string
	containsQ string
	addTag    string
}

func newLocalFilterCreateFlags() (*flag.FlagSet, *localFilterCreateFlags) {
	opts := &localFilterCreateFlags{}
	fs := flag.NewFlagSet("filter create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.name, "name", "", "name")
	fs.StringVar(&opts.containsQ, "contains", "", "subject/body contains")
	fs.StringVar(&opts.addTag, "add-tag", "", "tag to add")
	return fs, opts
}

type localSearchFlags struct {
	query string
}

func newLocalSearchFlags() (*flag.FlagSet, *localSearchFlags) {
	opts := &localSearchFlags{}
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.query, "query", "", "query")
	return fs, opts
}

type mailboxResolveFlags struct {
	name string
}

func newMailboxResolveFlags() (*flag.FlagSet, *mailboxResolveFlags) {
	opts := &mailboxResolveFlags{}
	fs := flag.NewFlagSet("mailbox resolve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.name, "name", "", "mailbox id or name")
	return fs, opts
}

type bridgeAccountUseFlags struct {
	username string
}

func newBridgeAccountUseFlags() (*flag.FlagSet, *bridgeAccountUseFlags) {
	opts := &bridgeAccountUseFlags{}
	fs := flag.NewFlagSet("bridge account use", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.username, "username", "", "bridge account username/email")
	return fs, opts
}

type authLoginFlags struct {
	username     string
	passwordFile string
}

func newAuthLoginFlags() (*flag.FlagSet, *authLoginFlags) {
	opts := &authLoginFlags{}
	fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.username, "username", "", "Bridge username/email")
	fs.StringVar(&opts.passwordFile, "password-file", "", "path to Bridge password file")
	return fs, opts
}
