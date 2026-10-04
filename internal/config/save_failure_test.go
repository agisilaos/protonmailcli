//go:build darwin || linux

package config

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestConfigSavePreservesPreviousFile(t *testing.T) {
	if path := os.Getenv("CONFIG_FAILURE_PATH"); path != "" {
		var original syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Fatal(err)
		}
		limited := original
		limited.Cur = 128
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
				t.Error(err)
			}
		}()
		value := strings.Repeat("synthetic", 100)
		if err := Save(path, Config{Bridge: Bridge{Username: value}}); !errors.Is(err, syscall.EFBIG) {
			t.Fatalf("expected file-size failure, got %v", err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "config.toml")

	save := func(value string) error { return Save(path, Config{Bridge: Bridge{Username: value}}) }
	load := func() (string, error) { cfg, err := Load(path); return cfg.Bridge.Username, err }
	if err := save("retained"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestConfigSavePreservesPreviousFile$")
	cmd.Env = append(os.Environ(), "CONFIG_FAILURE_PATH="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v %s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed write changed previous configuration")
	}
	if value, err := load(); err != nil || value != "retained" {
		t.Fatalf("reload after failure: %q, %v", value, err)
	}
	if err := save("recovered"); err != nil {
		t.Fatal(err)
	}
	if value, err := load(); err != nil || value != "recovered" {
		t.Fatalf("recovery: %q, %v", value, err)
	}
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("staging files remain: %v, %v", temps, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %v", info.Mode())
	}
}
