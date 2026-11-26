//go:build unix

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

const recordLockArgument = "--protonmailcli-internal-record-lock"

// POSIX record locks belong to a process and any close of the same inode in
// that process releases them. Own the lock in a helper that never opens inputs.
// The inherited control pipe releases it on Close or abrupt parent exit.
func lockRecordWriter(statePath string) (io.Closer, error) {
	var owner *recordWriterLock
	file, err := lockUnixWriterFileWith(statePath, func(file *os.File) error {
		var err error
		owner, err = startRecordWriter(file)
		return err
	})
	if err != nil {
		if owner != nil {
			_ = owner.Close()
		}
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = owner.Close()
		return nil, err
	}
	return owner, nil
}

type recordWriterLock struct {
	control io.Closer
	command *exec.Cmd
	once    sync.Once
	err     error
}

func (lock *recordWriterLock) Close() error {
	lock.once.Do(func() {
		lock.err = errors.Join(lock.control.Close(), lock.command.Wait())
	})
	return lock.err
}

type recordLockReply struct {
	Busy  bool
	Errno syscall.Errno
	Error string
}

func startRecordWriter(file *os.File) (*recordWriterLock, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	command := exec.Command(executable, recordLockArgument)
	command.ExtraFiles = []*os.File{file}
	// Terminal signals go to the caller. The helper follows its pipe lifetime.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	reader, input, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	command.Stdin = reader
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	defer output.Close()
	if err := command.Start(); err != nil {
		input.Close()
		return nil, fmt.Errorf("start state lock owner: %w", err)
	}
	owner := &recordWriterLock{control: input, command: command}
	var reply recordLockReply
	if err := json.NewDecoder(io.LimitReader(output, 4096)).Decode(&reply); err != nil {
		_ = owner.Close()
		return nil, fmt.Errorf("read state lock owner response: %w", err)
	}
	if reply.Busy || reply.Errno != 0 || reply.Error != "" {
		_ = owner.Close()
		if reply.Busy {
			return nil, ErrBusy
		}
		if reply.Errno != 0 {
			return nil, reply.Errno
		}
		return nil, errors.New(reply.Error)
	}
	return owner, nil
}

// Called during initialization only on record-lock platforms (and by tests).
// Re-execution inherits an already validated file, never a caller-supplied path.
func runRecordLockHelper() {
	if len(os.Args) != 2 || os.Args[1] != recordLockArgument {
		return
	}
	os.Exit(serveRecordLock())
}

func serveRecordLock() int {
	file := os.NewFile(3, "inherited state lock")
	defer file.Close()
	_, err := statWriterLock(file)
	if err == nil {
		err = tryRecordFileLock(file)
	}
	var reply recordLockReply
	if err != nil {
		reply.Busy = errors.Is(err, ErrBusy)
		errors.As(err, &reply.Errno)
		reply.Error = err.Error()
	}
	if err := json.NewEncoder(os.Stdout).Encode(reply); err != nil {
		return 1
	}
	if err == nil {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	return 0
}

func tryRecordFileLock(file *os.File) error {
	for {
		err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &unix.Flock_t{Type: unix.F_WRLCK, Whence: io.SeekStart})
		switch err {
		case unix.EINTR:
			continue
		case unix.EAGAIN, unix.EACCES:
			return ErrBusy
		default:
			return err
		}
	}
}
