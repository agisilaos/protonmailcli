package app

import (
	"fmt"
	"strings"
	"time"
)

func resolveManifestInput(file string, fromStdin bool) (string, error) {
	trimmed := strings.TrimSpace(file)
	if trimmed == "" && !fromStdin {
		return "", fmt.Errorf("one of --file or --stdin is required")
	}
	if trimmed != "" && fromStdin {
		return "", fmt.Errorf("provide only one of --file or --stdin")
	}
	if fromStdin {
		return "-", nil
	}
	return trimmed, nil
}

func parseRequiredUID(raw, flagName string) (string, error) {
	uid, err := parseUID(raw)
	if err != nil {
		return "", fmt.Errorf("%s required", flagName)
	}
	return uid, nil
}

func parseDateInput(s string) (time.Time, bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true, nil
	}
	return time.Time{}, false, fmt.Errorf("invalid date %q (expected YYYY-MM-DD or RFC3339)", s)
}
