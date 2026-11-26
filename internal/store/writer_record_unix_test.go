//go:build unix

package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func init() {
	runRecordLockHelper()
}

func TestRecordWriterSurvivesOtherDescriptorClose(t *testing.T) {
	const pathEnv = "PMAIL_TEST_RECORD_LOCK"
	if path := os.Getenv(pathEnv); path != "" {
		// A previous-version writer uses the same POSIX record-lock protocol.
		file, err := os.OpenFile(path+".lock", os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := tryRecordFileLock(file); !errors.Is(err, ErrBusy) {
			t.Fatalf("another process acquired a held writer lock: %v", err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "state.json")
	writer, err := lockRecordWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if other, err := lockRecordWriter(path); !errors.Is(err, ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("second writer in the same process: %v", err)
	}
	// This is the same open/read/close sequence as an empty draft body file.
	if _, err := os.ReadFile(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestRecordWriterSurvivesOtherDescriptorClose$")
	child.Env = append(os.Environ(), pathEnv+"="+path)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("record lock did not cover the input read: %v\n%s", err, out)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := lockRecordWriter(path)
	if err != nil {
		t.Fatalf("writer lock was not released: %v", err)
	}
	next.Close()
}

func TestRecordWriterRespectsExistingProcessLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := tryRecordFileLock(file); err != nil {
		t.Fatal(err)
	}
	if writer, err := lockRecordWriter(path); !errors.Is(err, ErrBusy) {
		if writer != nil {
			writer.Close()
		}
		t.Fatalf("ignored an existing process lock: %v", err)
	}
}

func TestRecordWriterPreservesReadOnlyDescriptorError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	owner, err := startRecordWriter(file)
	if owner != nil {
		owner.Close()
	}
	if !errors.Is(err, syscall.EBADF) {
		t.Fatalf("lost the record-lock write-access error: %v", err)
	}
}

func TestRecordWriterParentExitReleasesLock(t *testing.T) {
	const pathEnv = "PMAIL_TEST_RECORD_PARENT"
	if path := os.Getenv(pathEnv); path != "" {
		writer, err := lockRecordWriter(path)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		fmt.Fprintln(os.Stdout, "ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	path := filepath.Join(t.TempDir(), "state.json")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecordWriterParentExitReleasesLock$")
	child.Env = append(os.Environ(), pathEnv+"="+path)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("writer did not start: %q, %v", line, err)
	}
	if other, err := lockRecordWriter(path); !errors.Is(err, ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("owner did not hold its lock: %v", err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for {
		writer, err := lockRecordWriter(path)
		if err == nil {
			writer.Close()
			return
		}
		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			t.Fatalf("lock survived its parent's exit: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
