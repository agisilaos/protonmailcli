package app

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"protonmailcli/internal/config"
	"protonmailcli/internal/model"
)

func cmdAuth(action string, args []string, g globalOptions, cfg config.Config, st *model.State) (any, bool, error) {
	switch action {
	case "status":
		return authStatusResponse{
			LoggedIn:     st.Auth.LoggedIn,
			Username:     coalesce(st.Auth.Username, cfg.Bridge.Username),
			PasswordFile: coalesce(st.Auth.PasswordFile, cfg.Bridge.PasswordFile),
		}, false, nil
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
		return authLoginResponse{LoggedIn: true, Username: user}, true, nil
	case "logout":
		st.Auth.LoggedIn = false
		now := time.Now().UTC()
		st.Auth.LastLogoutAt = &now
		return authLoginResponse{LoggedIn: false}, true, nil
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
		out := []bridgeAccountItem{}
		for _, u := range []string{cfg.Bridge.Username, st.Auth.Username, st.Bridge.ActiveUsername} {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			if _, ok := seen[u]; ok {
				continue
			}
			seen[u] = struct{}{}
			out = append(out, bridgeAccountItem{
				Username: u,
				Active:   strings.TrimSpace(st.Bridge.ActiveUsername) != "" && st.Bridge.ActiveUsername == u,
			})
		}
		return bridgeAccountListResponse{
			Accounts: out,
			Count:    len(out),
			Active:   strings.TrimSpace(st.Bridge.ActiveUsername),
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
		resp := bridgeAccountUseResponse{Changed: true}
		resp.Active.Username = u
		return resp, true, nil
	default:
		return nil, false, cliError{exit: 2, code: "usage_error", msg: "bridge account supports list|use"}
	}
}
