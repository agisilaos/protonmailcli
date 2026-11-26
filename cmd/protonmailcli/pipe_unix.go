//go:build unix

package main

import (
	"os/signal"
	"syscall"
)

func init() {
	// Let the CLI report a closed output pipe and preserve the command's exit code.
	signal.Ignore(syscall.SIGPIPE)
}
