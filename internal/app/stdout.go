package app

import (
	"fmt"
	"io"
)

// commandOutput retains the first write failure, including ignored renderer errors.
// Completed command work is never retried because its receipt could not be delivered.
type commandOutput struct {
	writer io.Writer
	err    error
}

func (w *commandOutput) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}

func (a App) run(args []string) int {
	out := &commandOutput{writer: a.Stdout}
	a.Stdout = out
	code := a.runCommand(args)
	if out.err != nil {
		fmt.Fprintf(a.Stderr, "stdout write failed: %v; any completed changes remain applied; inspect state before retrying\n", out.err)
		if code == 0 {
			return 1
		}
	}
	return code
}
