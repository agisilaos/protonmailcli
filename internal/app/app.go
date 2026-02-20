package app

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"protonmailcli/internal/bridge"
	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
	"protonmailcli/internal/output"
	"protonmailcli/internal/store"
)

type App struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
}

type globalOptions struct {
	mode      output.Mode
	noInput   bool
	profile   string
	dryRun    bool
	showHelp  bool
	showVer   bool
	config    string
	statePath string
}

type cliError struct {
	exit int
	code string
	msg  string
	hint string
}

func (e cliError) Error() string { return e.msg }

var readAllStdinFn = func() ([]byte, error) {
	return io.ReadAll(runtimeStdinReader)
}

var (
	runtimeStdinReader io.Reader = os.Stdin
	runtimeStdout      io.Writer = os.Stdout
	runtimeStderr      io.Writer = os.Stderr
	runtimeStdinIsTTY            = func() bool { return isTTY(os.Stdin) }
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func Run(args []string, in io.Reader, out io.Writer, errw io.Writer) int {
	a := App{Stdout: out, Stderr: errw, Stdin: in}
	return a.run(args)
}

func bindRuntimeIO(a App) func() {
	prevIn := runtimeStdinReader
	prevOut := runtimeStdout
	prevErr := runtimeStderr
	prevTTY := runtimeStdinIsTTY

	runtimeStdinReader = a.Stdin
	runtimeStdout = a.Stdout
	runtimeStderr = a.Stderr
	runtimeStdinIsTTY = func() bool {
		if f, ok := a.Stdin.(*os.File); ok {
			return isTTY(f)
		}
		return false
	}

	return func() {
		runtimeStdinReader = prevIn
		runtimeStdout = prevOut
		runtimeStderr = prevErr
		runtimeStdinIsTTY = prevTTY
	}
}

func (a App) run(args []string) int {
	restoreIO := bindRuntimeIO(a)
	defer restoreIO()

	start := time.Now()
	requestID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	g, rest, err := parseGlobal(args)
	if err != nil {
		mode := g.mode
		if mode == "" {
			mode = output.ModeHuman
		}
		_ = output.PrintError(a.Stdout, mode, "usage_error", err.Error(), "Use --help for usage", "usage", false, g.profile, requestID, start)
		return 2
	}
	if g.showVer {
		fmt.Fprintf(a.Stdout, "protonmailcli %s (%s) %s\n", Version, Commit, Date)
		return 0
	}
	if g.showHelp || len(rest) == 0 {
		printHelp(a.Stdout)
		return 0
	}
	if err := validateNoLateGlobalFlags(rest); err != nil {
		return a.exitWithError(err, fallbackMode(g.mode), g.profile, requestID, start)
	}

	cfgPath := g.config
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	if g.statePath == "" {
		g.statePath = config.DefaultStatePath()
	}

	if rest[0] == "completion" {
		if err := cmdCompletion(a.Stdout, rest[1:]); err != nil {
			return a.exitWithError(err, fallbackMode(g.mode), g.profile, requestID, start)
		}
		return 0
	}

	if rest[0] == "setup" {
		if err := a.cmdSetup(rest[1:], g, cfgPath); err != nil {
			return a.exitWithError(err, fallbackMode(g.mode), g.profile, requestID, start)
		}
		_ = output.PrintSuccess(a.Stdout, fallbackMode(g.mode), map[string]any{"configured": true, "configPath": cfgPath}, g.profile, requestID, start)
		return 0
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return a.exitWithError(cliError{exit: 3, code: "config_missing", msg: "configuration not found", hint: "Run protonmailcli setup first"}, fallbackMode(g.mode), g.profile, requestID, start)
	}
	if g.profile == "" {
		g.profile = cfg.Profile
	}
	if g.mode == "" {
		g.mode = output.Mode(cfg.Output)
	}
	if g.mode == "" {
		g.mode = output.ModeHuman
	}

	st := store.New(g.statePath)
	state, err := st.Load()
	if err != nil {
		return a.exitWithError(cliError{exit: 1, code: "state_error", msg: err.Error()}, g.mode, g.profile, requestID, start)
	}

	data, changed, err := a.dispatch(rest, g, cfg, &state)
	if err != nil {
		return a.exitWithError(err, g.mode, g.profile, requestID, start)
	}
	exitCode := normalizeExitCode(data)
	if changed && !g.dryRun {
		if err := st.Save(state); err != nil {
			return a.exitWithError(cliError{exit: 1, code: "state_save_failed", msg: err.Error()}, g.mode, g.profile, requestID, start)
		}
	}
	if g.dryRun {
		fmt.Fprintln(a.Stderr, "dry-run: no changes applied")
	}
	_ = output.PrintSuccess(a.Stdout, g.mode, data, g.profile, requestID, start)
	return exitCode
}

func fallbackMode(m output.Mode) output.Mode {
	if m == "" {
		return output.ModeHuman
	}
	return m
}

func normalizeExitCode(data any) int {
	type exitCoder interface {
		ExitCode() int
	}
	if withExit, ok := data.(exitCoder); ok {
		if code := withExit.ExitCode(); code > 0 {
			return code
		}
	}
	m, ok := data.(map[string]any)
	if !ok {
		return 0
	}
	raw, exists := m["_exitCode"]
	if !exists {
		return 0
	}
	delete(m, "_exitCode")
	switch v := raw.(type) {
	case int:
		if v > 0 {
			return v
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	}
	return 0
}

func (a App) exitWithError(err error, mode output.Mode, profile, requestID string, start time.Time) int {
	var ce cliError
	if errors.As(err, &ce) {
		if ce.hint != "" {
			fmt.Fprintln(a.Stderr, ce.hint)
		}
		classified := classifyCLIError(ce.code, ce.exit)
		_ = output.PrintError(a.Stdout, mode, ce.code, ce.msg, ce.hint, classified.Category, classified.Retryable, profile, requestID, start)
		return ce.exit
	}
	_ = output.PrintError(a.Stdout, mode, "runtime_error", err.Error(), "", "runtime", false, profile, requestID, start)
	return 1
}

func parseGlobal(args []string) (globalOptions, []string, error) {
	g := globalOptions{}
	i := 0
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			break
		}
		switch a {
		case "--json":
			g.mode = output.ModeJSON
		case "--plain":
			g.mode = output.ModePlain
		case "--no-input":
			g.noInput = true
		case "--dry-run", "-n":
			g.dryRun = true
		case "--help", "-h":
			g.showHelp = true
		case "--version":
			g.showVer = true
		case "--profile":
			i++
			if i >= len(args) {
				return g, nil, fmt.Errorf("missing value for --profile")
			}
			g.profile = args[i]
		case "--config":
			i++
			if i >= len(args) {
				return g, nil, fmt.Errorf("missing value for --config")
			}
			g.config = args[i]
		case "--state":
			i++
			if i >= len(args) {
				return g, nil, fmt.Errorf("missing value for --state")
			}
			g.statePath = args[i]
		default:
			return g, nil, fmt.Errorf("unknown global flag: %s", a)
		}
		i++
	}
	return g, args[i:], nil
}

func validateNoLateGlobalFlags(rest []string) error {
	lateGlobals := map[string]bool{
		"--json":     true,
		"--plain":    true,
		"--no-input": true,
		"--dry-run":  true,
		"-n":         true,
	}
	for _, a := range rest[1:] {
		if lateGlobals[a] {
			return cliError{
				exit: 2,
				code: "usage_error",
				msg:  fmt.Sprintf("global flag %s must appear before the resource", a),
				hint: "Example: protonmailcli --json draft list",
			}
		}
	}
	return nil
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, `protonmailcli - Proton Mail Bridge CLI

Usage:
  protonmailcli [global flags] <resource> <action> [args]
  protonmailcli setup [flags]
  protonmailcli doctor
  protonmailcli completion <bash|zsh|fish>

Resources:
  setup
  bridge     account list|use
  auth       login|status|logout
  draft      create|create-many|update|get|list|delete
  message    send|send-many|get
  search     messages|drafts
  mailbox    list|resolve
  tag        list|create|add|remove
  filter     list|create|delete|test|apply

Global flags:
  --json --plain --no-input --dry-run --profile <name> --config <path> --state <path>
  -h, --help  --version`)
}

func (a App) cmdSetup(args []string, g globalOptions, cfgPath string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	interactive := fs.Bool("interactive", false, "interactive prompts")
	nonInteractive := fs.Bool("non-interactive", false, "disable prompts")
	host := fs.String("bridge-host", "127.0.0.1", "Bridge host")
	smtpPort := fs.Int("bridge-smtp-port", 1025, "Bridge SMTP port")
	imapPort := fs.Int("bridge-imap-port", 1143, "Bridge IMAP port")
	username := fs.String("username", "", "Bridge username/email")
	passwordFile := fs.String("smtp-password-file", "", "path to Bridge SMTP password file")
	profile := fs.String("profile", "default", "Profile name")
	if err := fs.Parse(args); err != nil {
		return cliError{exit: 2, code: "usage_error", msg: err.Error()}
	}
	useInteractive := *interactive || (!*nonInteractive && !g.noInput && runtimeStdinIsTTY())
	cfg := config.Default()
	cfg.Profile = *profile
	if useInteractive {
		r := bufio.NewReader(a.Stdin)
		fmt.Fprint(a.Stderr, "Profile [default]: ")
		if v, _ := r.ReadString('\n'); strings.TrimSpace(v) != "" {
			cfg.Profile = strings.TrimSpace(v)
		}
		fmt.Fprint(a.Stderr, "Bridge host [127.0.0.1]: ")
		if v, _ := r.ReadString('\n'); strings.TrimSpace(v) != "" {
			cfg.Bridge.Host = strings.TrimSpace(v)
		}
		fmt.Fprint(a.Stderr, "Bridge SMTP port [1025]: ")
		if v, _ := r.ReadString('\n'); strings.TrimSpace(v) != "" {
			fmt.Sscanf(strings.TrimSpace(v), "%d", &cfg.Bridge.SMTPPort)
		}
		fmt.Fprint(a.Stderr, "Bridge IMAP port [1143]: ")
		if v, _ := r.ReadString('\n'); strings.TrimSpace(v) != "" {
			fmt.Sscanf(strings.TrimSpace(v), "%d", &cfg.Bridge.IMAPPort)
		}
		fmt.Fprint(a.Stderr, "Bridge username/email: ")
		if v, _ := r.ReadString('\n'); strings.TrimSpace(v) != "" {
			cfg.Bridge.Username = strings.TrimSpace(v)
		}
		fmt.Fprint(a.Stderr, "SMTP password file path (optional): ")
		if v, _ := r.ReadString('\n'); strings.TrimSpace(v) != "" {
			cfg.Bridge.PasswordFile = strings.TrimSpace(v)
		}
	} else {
		cfg.Bridge.Host = *host
		cfg.Bridge.SMTPPort = *smtpPort
		cfg.Bridge.IMAPPort = *imapPort
		cfg.Bridge.Username = *username
		cfg.Bridge.PasswordFile = *passwordFile
		if cfg.Bridge.Username == "" {
			return cliError{exit: 2, code: "validation_error", msg: "--username is required in non-interactive setup", hint: "Pass --username or use --interactive"}
		}
	}
	return config.Save(cfgPath, cfg)
}

func (a App) dispatch(rest []string, g globalOptions, cfg config.Config, state *model.State) (any, bool, error) {
	resource := rest[0]
	action := ""
	args := []string{}
	if len(rest) > 1 {
		action = rest[1]
		args = rest[2:]
	}

	switch resource {
	case "doctor":
		if action != "" {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: "doctor does not take an action"}
		}
		return cmdDoctor(cfg, state)
	case "auth":
		if action == "" {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: "auth action required"}
		}
		return cmdAuth(action, args, g, cfg, state)
	case "bridge":
		if action == "" {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: "bridge action required"}
		}
		return cmdBridge(action, args, cfg, state)
	case "mailbox":
		if action == "" {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: "mailbox action required"}
		}
		return dispatchMailbox(action, args, g, cfg, state)
	case "draft":
		if action == "" {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: "draft action required"}
		}
		return dispatchDraft(action, args, g, cfg, state)
	case "message":
		if action == "" {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: "message action required"}
		}
		return dispatchMessage(action, args, g, cfg, state)
	case "search":
		if action == "" {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: "search action required"}
		}
		return dispatchSearch(action, args, g, cfg, state)
	case "tag":
		if action == "" {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: "tag action required"}
		}
		return dispatchTag(action, args, g, cfg, state)
	case "filter":
		if action == "" {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: "filter action required"}
		}
		return cmdFilter(action, args, g, state)
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown resource: " + resource}
	}
}

func dispatchMailbox(action string, args []string, g globalOptions, cfg config.Config, state *model.State) (any, bool, error) {
	if useLocalStateMode() {
		return cmdMailbox(action, args, g, state)
	}
	return cmdMailboxIMAP(action, args, g, cfg, state)
}

func dispatchDraft(action string, args []string, g globalOptions, cfg config.Config, state *model.State) (any, bool, error) {
	if useLocalStateMode() {
		return cmdDraft(action, args, g, state)
	}
	return cmdDraftIMAP(action, args, g, cfg, state)
}

func dispatchMessage(action string, args []string, g globalOptions, cfg config.Config, state *model.State) (any, bool, error) {
	if useLocalStateMode() {
		return cmdMessage(action, args, g, cfg, state)
	}
	return cmdMessageIMAP(action, args, g, cfg, state)
}

func dispatchSearch(action string, args []string, g globalOptions, cfg config.Config, state *model.State) (any, bool, error) {
	if useLocalStateMode() {
		return cmdSearch(action, args, g, state)
	}
	return cmdSearchIMAP(action, args, g, cfg, state)
}

func dispatchTag(action string, args []string, g globalOptions, cfg config.Config, state *model.State) (any, bool, error) {
	if useLocalStateMode() {
		return cmdTag(action, args, g, state)
	}
	return cmdTagIMAP(action, args, g, cfg, state)
}

type sliceFlag []string

func (s *sliceFlag) String() string { return strings.Join(*s, ",") }
func (s *sliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func cmdDoctor(cfg config.Config, st *model.State) (any, bool, error) {
	timeout := 3 * time.Second
	smtp := bridge.CheckTCP(cfg.Bridge.Host, cfg.Bridge.SMTPPort, timeout, "smtp")
	imap := bridge.CheckTCP(cfg.Bridge.Host, cfg.Bridge.IMAPPort, timeout, "imap")
	bridgeOK := smtp.OK && imap.OK
	authDetails, authOK := doctorAuthPrereqs(cfg, st)
	configDetails, configOK := doctorConfigPrereqs(cfg)
	ok := bridgeOK && authOK && configOK
	data := map[string]any{
		"ok":     ok,
		"checks": []bridge.HealthStatus{smtp, imap}, // backwards-compatible top-level key
		"summary": map[string]any{
			"bridge":      bridgeOK,
			"authPrereqs": authOK,
			"config":      configOK,
		},
		"doctor": map[string]any{
			"bridge": map[string]any{"ok": bridgeOK, "checks": []bridge.HealthStatus{smtp, imap}},
			"auth":   authDetails,
			"config": configDetails,
		},
	}
	if !configOK || !authOK {
		return data, false, cliError{
			exit: 3,
			code: "doctor_prereq_failed",
			msg:  "one or more doctor prerequisites are not satisfied",
			hint: "Run setup and auth login, then retry doctor",
		}
	}
	if !bridgeOK {
		return data, false, cliError{exit: 4, code: "bridge_unreachable", msg: "one or more bridge endpoints are unreachable", hint: "Check Proton Mail Bridge is running and ports match setup"}
	}
	return data, false, nil
}

func doctorConfigPrereqs(cfg config.Config) (map[string]any, bool) {
	missing := []string{}
	if strings.TrimSpace(cfg.Bridge.Host) == "" {
		missing = append(missing, "bridge.host")
	}
	if cfg.Bridge.IMAPPort <= 0 {
		missing = append(missing, "bridge.imap_port")
	}
	if cfg.Bridge.SMTPPort <= 0 {
		missing = append(missing, "bridge.smtp_port")
	}
	return map[string]any{
		"ok":      len(missing) == 0,
		"missing": missing,
	}, len(missing) == 0
}

func doctorAuthPrereqs(cfg config.Config, st *model.State) (map[string]any, bool) {
	username := firstNonEmpty(st.Auth.Username, cfg.Bridge.Username)
	passwordFromEnv := strings.TrimSpace(os.Getenv("PMAIL_SMTP_PASSWORD")) != ""
	passwordFile := firstNonEmpty(st.Auth.PasswordFile, cfg.Bridge.PasswordFile)
	passwordFileReadable := false
	if strings.TrimSpace(passwordFile) != "" {
		_, err := os.Stat(filepath.Clean(config.Expand(passwordFile)))
		passwordFileReadable = err == nil
	}
	ok := strings.TrimSpace(username) != "" && (passwordFromEnv || passwordFileReadable)
	return map[string]any{
		"ok":                     ok,
		"usernameConfigured":     strings.TrimSpace(username) != "",
		"passwordFromEnv":        passwordFromEnv,
		"passwordFileConfigured": strings.TrimSpace(passwordFile) != "",
		"passwordFileReadable":   passwordFileReadable,
	}, ok
}

func cmdCompletion(w io.Writer, args []string) error {
	if len(args) < 1 {
		return cliError{exit: 2, code: "usage_error", msg: "completion shell required (bash|zsh|fish)"}
	}
	shell := args[0]
	switch shell {
	case "bash":
		_, err := fmt.Fprintln(w, bashCompletion())
		return err
	case "zsh":
		_, err := fmt.Fprintln(w, zshCompletion())
		return err
	case "fish":
		_, err := fmt.Fprintln(w, fishCompletion())
		return err
	default:
		return cliError{exit: 2, code: "usage_error", msg: "unsupported shell: " + shell}
	}
}

func cmdAuth(action string, args []string, g globalOptions, cfg config.Config, st *model.State) (any, bool, error) {
	switch action {
	case "status":
		return map[string]any{"loggedIn": st.Auth.LoggedIn, "username": coalesce(st.Auth.Username, cfg.Bridge.Username), "passwordFile": coalesce(st.Auth.PasswordFile, cfg.Bridge.PasswordFile)}, false, nil
	case "login":
		fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		username := fs.String("username", "", "Bridge username/email")
		passwordFile := fs.String("password-file", "", "path to Bridge password file")
		if err := fs.Parse(args); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		user := *username
		passFile := *passwordFile
		if user == "" {
			user = cfg.Bridge.Username
		}
		if passFile == "" {
			passFile = cfg.Bridge.PasswordFile
		}
		if user == "" || passFile == "" {
			if g.noInput || !runtimeStdinIsTTY() {
				return nil, false, cliError{exit: 2, code: "validation_error", msg: "--username and --password-file are required in non-interactive mode"}
			}
			r := bufio.NewReader(runtimeStdinReader)
			if user == "" {
				fmt.Fprint(runtimeStderr, "Bridge username/email: ")
				v, _ := r.ReadString('\n')
				user = strings.TrimSpace(v)
			}
			if passFile == "" {
				fmt.Fprint(runtimeStderr, "Bridge password file path: ")
				v, _ := r.ReadString('\n')
				passFile = strings.TrimSpace(v)
			}
		}
		if user == "" || passFile == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "username and password-file are required"}
		}
		if _, err := os.Stat(config.Expand(passFile)); err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "password-file not readable"}
		}
		now := time.Now().UTC()
		st.Auth = model.AuthState{LoggedIn: true, Username: user, PasswordFile: passFile, LastLoginAt: &now}
		return map[string]any{"loggedIn": true, "username": user}, true, nil
	case "logout":
		st.Auth.LoggedIn = false
		now := time.Now().UTC()
		st.Auth.LastLogoutAt = &now
		return map[string]any{"loggedIn": false}, true, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown auth action: " + action}
	}
}

func cmdBridge(action string, args []string, cfg config.Config, st *model.State) (any, bool, error) {
	if action != "account" {
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "bridge supports account"}
	}
	if len(args) == 0 {
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "bridge account action required (list|use)"}
	}
	switch args[0] {
	case "list":
		seen := map[string]struct{}{}
		out := []map[string]any{}
		for _, u := range []string{cfg.Bridge.Username, st.Auth.Username, st.Bridge.ActiveUsername} {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			if _, ok := seen[u]; ok {
				continue
			}
			seen[u] = struct{}{}
			out = append(out, map[string]any{
				"username": u,
				"active":   strings.TrimSpace(st.Bridge.ActiveUsername) != "" && st.Bridge.ActiveUsername == u,
			})
		}
		return map[string]any{
			"accounts": out,
			"count":    len(out),
			"active":   strings.TrimSpace(st.Bridge.ActiveUsername),
		}, false, nil
	case "use":
		fs := flag.NewFlagSet("bridge account use", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		username := fs.String("username", "", "bridge account username/email")
		if err := fs.Parse(args[1:]); err != nil {
			return nil, false, cliError{exit: 2, code: "usage_error", msg: err.Error()}
		}
		u := strings.TrimSpace(*username)
		if u == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--username is required"}
		}
		st.Bridge.ActiveUsername = u
		return map[string]any{
			"active": map[string]any{
				"username": u,
			},
			"changed": true,
		}, true, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "bridge account supports list|use"}
	}
}

func cmdMailbox(action string, args []string, g globalOptions, st *model.State) (any, bool, error) {
	boxes := []mailboxInfo{
		{ID: "inbox", Name: "INBOX", Kind: "system", Count: len(st.Messages)},
		{ID: "drafts", Name: "Drafts", Kind: "system", Count: len(st.Drafts)},
		{ID: "sent", Name: "Sent", Kind: "system", Count: countSent(st.Messages)},
	}
	if action == "resolve" {
		fs := flag.NewFlagSet("mailbox resolve", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		_ = fs.String("name", "", "mailbox id or name")
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "mailbox resolve", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
	}
	return mailboxAction(action, args, boxes, "local")
}

func countSent(msgs map[string]model.Message) int {
	return len(msgs)
}

func cmdSearch(action string, args []string, g globalOptions, st *model.State) (any, bool, error) {
	if action != "messages" && action != "drafts" {
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "search supports messages|drafts"}
	}
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	query := fs.String("query", "", "query")
	if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "search "+action, runtimeStdout); err != nil {
		return nil, false, err
	} else if handled {
		return helpData, false, nil
	}
	q := strings.ToLower(*query)
	if action == "drafts" {
		out := []model.Draft{}
		for _, d := range st.Drafts {
			if q == "" || strings.Contains(strings.ToLower(d.Subject+" "+d.Body+" "+strings.Join(d.To, " ")), q) {
				out = append(out, d)
			}
		}
		return map[string]any{"drafts": out, "count": len(out)}, false, nil
	}
	out := []model.Message{}
	for _, m := range st.Messages {
		if q == "" || strings.Contains(strings.ToLower(m.Subject+" "+m.Body+" "+strings.Join(m.To, " ")), q) {
			out = append(out, m)
		}
	}
	return map[string]any{"messages": out, "count": len(out)}, false, nil
}

func cmdTag(action string, args []string, g globalOptions, st *model.State) (any, bool, error) {
	switch action {
	case "list":
		list := make([]string, 0, len(st.Tags))
		for name := range st.Tags {
			list = append(list, name)
		}
		sort.Strings(list)
		return map[string]any{"tags": list, "count": len(list)}, false, nil
	case "create":
		fs := flag.NewFlagSet("tag create", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		name := fs.String("name", "", "tag name")
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "tag create", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		if *name == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--name required"}
		}
		id := st.Tags[*name]
		changed := false
		if id == "" {
			id = fmt.Sprintf("t_%d", time.Now().UnixNano())
			st.Tags[*name] = id
			changed = true
		}
		return map[string]any{"tag": map[string]string{"id": id, "name": *name}, "changed": changed}, changed, nil
	case "add", "remove":
		fs := flag.NewFlagSet("tag add/remove", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		msgID := fs.String("message-id", "", "message id")
		tag := fs.String("tag", "", "tag name")
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "tag "+action, runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		uid, err := parseRequiredUID(*msgID, "--message-id")
		if err != nil {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: err.Error()}
		}
		m, ok := st.Messages[uid]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "message not found"}
		}
		if *tag == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--tag required"}
		}
		changed := false
		if action == "add" {
			if !contains(m.Tags, *tag) {
				m.Tags = append(m.Tags, *tag)
				changed = true
			}
		} else {
			next := make([]string, 0, len(m.Tags))
			for _, t := range m.Tags {
				if t != *tag {
					next = append(next, t)
				} else {
					changed = true
				}
			}
			m.Tags = next
		}
		st.Messages[uid] = m
		return map[string]any{"messageId": uid, "tag": *tag, "changed": changed}, changed, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown tag action: " + action}
	}
}

func cmdFilter(action string, args []string, g globalOptions, st *model.State) (any, bool, error) {
	switch action {
	case "list":
		filters := make([]model.Filter, 0, len(st.Filters))
		for _, f := range st.Filters {
			filters = append(filters, f)
		}
		return map[string]any{"filters": filters, "count": len(filters)}, false, nil
	case "create":
		fs := flag.NewFlagSet("filter create", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		name := fs.String("name", "", "name")
		containsQ := fs.String("contains", "", "subject/body contains")
		addTag := fs.String("add-tag", "", "tag to add")
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "filter create", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		if *name == "" || *containsQ == "" || *addTag == "" {
			return nil, false, cliError{exit: 2, code: "validation_error", msg: "--name, --contains and --add-tag are required"}
		}
		id := fmt.Sprintf("f_%d", time.Now().UnixNano())
		f := model.Filter{ID: id, Name: *name, Contains: *containsQ, AddTag: *addTag, CreatedAt: time.Now().UTC()}
		if !g.dryRun {
			st.Filters[id] = f
		}
		return map[string]any{"filter": f}, true, nil
	case "delete":
		fs := flag.NewFlagSet("filter delete", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("filter-id", "", "filter id")
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "filter delete", runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		if _, ok := st.Filters[*id]; !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "filter not found"}
		}
		if !g.dryRun {
			delete(st.Filters, *id)
		}
		return map[string]any{"deleted": true, "filterId": *id}, true, nil
	case "test", "apply":
		fs := flag.NewFlagSet("filter test/apply", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("filter-id", "", "filter id")
		if helpData, handled, err := parseFlagSetWithHelp(fs, args, g, "filter "+action, runtimeStdout); err != nil {
			return nil, false, err
		} else if handled {
			return helpData, false, nil
		}
		f, ok := st.Filters[*id]
		if !ok {
			return nil, false, cliError{exit: 5, code: "not_found", msg: "filter not found"}
		}
		matches := 0
		changes := 0
		for id, m := range st.Messages {
			hay := strings.ToLower(m.Subject + " " + m.Body)
			if strings.Contains(hay, strings.ToLower(f.Contains)) {
				matches++
				if action == "apply" && !contains(m.Tags, f.AddTag) {
					m.Tags = append(m.Tags, f.AddTag)
					st.Messages[id] = m
					changes++
				}
			}
		}
		changed := action == "apply" && changes > 0
		return map[string]any{"filterId": f.ID, "mode": action, "matched": matches, "changed": changes}, changed, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "unknown filter action: " + action}
	}
}

func loadBody(body, bodyFile string, stdinBody bool) (string, error) {
	if stdinBody {
		if strings.TrimSpace(body) != "" || strings.TrimSpace(bodyFile) != "" {
			return "", fmt.Errorf("provide only one of --body, --body-file, or --stdin")
		}
		b, err := readAllStdinFn()
		return string(b), err
	}
	if body != "" && bodyFile != "" {
		return "", fmt.Errorf("provide only one of --body, --body-file, or --stdin")
	}
	if body != "" {
		return body, nil
	}
	if bodyFile == "" {
		return "", fmt.Errorf("one of --body, --body-file, or --stdin is required")
	}
	if bodyFile == "-" {
		b, err := readAllStdinFn()
		return string(b), err
	}
	b, err := os.ReadFile(filepath.Clean(bodyFile))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func coalesce(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func isTTY(r *os.File) bool {
	info, err := r.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func bashCompletion() string {
	return `# protonmailcli bash completion
_protonmailcli_completions()
{
  COMPREPLY=( $(compgen -W "setup doctor completion bridge auth draft message search mailbox tag filter" -- "${COMP_WORDS[1]}") )
}
complete -F _protonmailcli_completions protonmailcli`
}

func zshCompletion() string {
	return `#compdef protonmailcli
_arguments "1: :((setup doctor completion bridge auth draft message search mailbox tag filter))"`
}

func fishCompletion() string {
	return `complete -c protonmailcli -f -a "setup doctor completion bridge auth draft message search mailbox tag filter"`
}
