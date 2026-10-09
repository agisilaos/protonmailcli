package app

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"protonmailcli/internal/config"
)

func readPasswordFile(path string) (string, error) {
	// Nonblocking open also prevents a FIFO replacement between path inspection
	// and open from stalling the command. Only regular files are read below.
	f, err := os.OpenFile(config.Expand(path), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("password-file must be a regular file")
	}
	const maxPasswordBytes = 64 * 1024
	b, err := io.ReadAll(io.LimitReader(f, maxPasswordBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxPasswordBytes {
		return "", fmt.Errorf("password-file exceeds 64 KiB")
	}
	password := strings.TrimSpace(string(b))
	if password == "" {
		return "", fmt.Errorf("password-file must contain a nonempty password")
	}
	return password, nil
}
