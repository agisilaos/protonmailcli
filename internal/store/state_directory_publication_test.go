//go:build darwin || linux

package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"protonmailcli/internal/model"
)

func TestSecondInitializerSyncsParentBeforeAcceptingCheckpoint(t *testing.T) {
	for _, alias := range []bool{false, true} {
		t.Run(fmt.Sprintf("alias=%v", alias), func(t *testing.T) { testSecondInitializerSyncsParent(t, alias) })
	}
}

func testSecondInitializerSyncsParent(t *testing.T, alias bool) {
	root := t.TempDir()
	path := filepath.Join(root, "new", "state.json")
	previous := syncStateDirectory
	paused, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var parentSyncs atomic.Int32
	first := make(chan error, 1)
	syncStateDirectory = func(dir *os.File) error {
		if directoryFileIs(dir, root) && parentSyncs.Add(1) == 1 {
			close(paused)
			<-resume
		}
		return previous(dir)
	}
	go func() { first <- firstUseStateEntry(t, "LockWriter", New(path)) }()
	defer func() { once.Do(func() { close(resume) }); <-first; syncStateDirectory = previous }()
	select {
	case <-paused:
	case <-time.After(3 * time.Second):
		t.Fatal("first initializer did not reach parent sync")
	}
	secondPath := path
	if alias {
		link := filepath.Join(t.TempDir(), "directory-alias")
		if err := os.Symlink(filepath.Dir(path), link); err != nil {
			t.Fatal(err)
		}
		secondPath = filepath.Join(link, "state.json")
	}
	disk := New(secondPath)
	lock, err := disk.LockWriter()
	if err != nil {
		t.Fatal(err)
	}
	state := emptyState()
	state.Idempotency["synthetic-send"] = model.IdempotencyRecord{Operation: "message.send", Status: "pending"}
	saveErr := disk.Save(state)
	lock.Close()
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	if parentSyncs.Load() < 2 {
		t.Fatal("second writer accepted a pending checkpoint before either initializer synced its parent")
	}
	visible, err := disk.Load()
	if err != nil || visible.Idempotency["synthetic-send"].Status != "pending" {
		t.Fatalf("missing successful checkpoint: %+v %v", visible, err)
	}
}

func TestInterruptedDirectoryPublicationRecoversParentSync(t *testing.T) {
	if path := os.Getenv("PMAIL_TEST_DIRECTORY_PUBLICATION"); path != "" {
		previous := syncStateDirectory
		syncStateDirectory = func(dir *os.File) error {
			if directoryFileIs(dir, filepath.Dir(filepath.Dir(path))) {
				os.Exit(23)
			}
			return previous(dir)
		}
		if err := firstUseStateEntry(t, "LockWriter", New(path)); err != nil {
			t.Fatal(err)
		}
		t.Fatal("expected interruption before parent sync")
	}
	root := t.TempDir()
	path := filepath.Join(root, "new", "state.json")
	child := exec.Command(os.Args[0], "-test.run=^TestInterruptedDirectoryPublicationRecoversParentSync$")
	child.Env = append(os.Environ(), "PMAIL_TEST_DIRECTORY_PUBLICATION="+path)
	if out, err := child.CombinedOutput(); err == nil || child.ProcessState == nil || child.ProcessState.ExitCode() != 23 {
		t.Fatalf("unexpected child result: %v\n%s", err, out)
	}
	previous := syncStateDirectory
	t.Cleanup(func() { syncStateDirectory = previous })
	synced := false
	syncStateDirectory = func(dir *os.File) error {
		if directoryFileIs(dir, root) {
			synced = true
		}
		return previous(dir)
	}
	if err := firstUseStateEntry(t, "LockWriter", New(path)); err != nil {
		t.Fatal(err)
	}
	if !synced {
		t.Fatal("retry accepted interrupted first-use directory without syncing its parent")
	}
}

func TestDirectoryPublicationPreservesCreationModeAndGroup(t *testing.T) {
	if path := os.Getenv("PMAIL_TEST_DIRECTORY_UMASK"); path != "" {
		mask, err := strconv.ParseInt(os.Getenv("PMAIL_TEST_DIRECTORY_MASK"), 8, 32)
		if err != nil {
			t.Fatal(err)
		}
		syscall.Umask(int(mask))
		control := filepath.Join(filepath.Dir(filepath.Dir(path)), "normal-mkdir")
		if err := os.Mkdir(control, 0755); err != nil {
			t.Fatal(err)
		}
		if err := firstUseStateEntry(t, "LockWriter", New(path)); err != nil {
			t.Fatal(err)
		}
		normal, err := os.Stat(control)
		if err != nil {
			t.Fatal(err)
		}
		published, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if published.Mode() != normal.Mode() || published.Sys().(*syscall.Stat_t).Gid != normal.Sys().(*syscall.Stat_t).Gid {
			t.Fatalf("changed native creation permissions/group: normal=%v published=%v", normal, published)
		}
		return
	}
	for _, mask := range []string{"000", "022", "077"} {
		t.Run(mask, func(t *testing.T) {
			parent := t.TempDir()
			if err := os.Chmod(parent, 0770|os.ModeSetgid); err != nil {
				t.Fatal(err)
			}
			child := exec.Command(os.Args[0], "-test.run=^TestDirectoryPublicationPreservesCreationModeAndGroup$")
			child.Env = append(os.Environ(), "PMAIL_TEST_DIRECTORY_UMASK="+filepath.Join(parent, "new", "state.json"), "PMAIL_TEST_DIRECTORY_MASK="+mask)
			if out, err := child.CombinedOutput(); err != nil {
				t.Fatalf("creation under umask %s: %v\n%s", mask, err, out)
			}
		})
	}
}

func TestExistingDirectoryLeavesUnrelatedInitializationNamesUntouched(t *testing.T) {
	for _, kind := range []string{"file", "same-size file", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "shared")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, stateDirectoryMarker)
			original := "unrelated caller data"
			if kind == "same-size file" {
				original = strings.Repeat("x", len(stateDirectoryMarkerContent))
			}
			switch kind {
			case "file", "same-size file":
				if err := os.WriteFile(marker, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				other := filepath.Join(root, "original")
				if err := os.WriteFile(other, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, marker); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(marker, 0700); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(marker)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(root, 0100); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(root, 0700) })
			disk := New(filepath.Join(dir, "state.json"))
			if err := firstUseStateEntry(t, "LockWriter", disk); err != nil {
				t.Fatal(err)
			}
			if err := disk.Save(emptyState()); err != nil {
				t.Fatal(err)
			}
			after, err := os.Lstat(marker)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("unrelated name replaced: %v", err)
			}
			if kind != "directory" {
				data, err := os.ReadFile(marker)
				if err != nil || string(data) != original {
					t.Fatalf("unrelated contents changed: %q %v", data, err)
				}
			}
		})
	}
}

func TestDirectoryPublicationRefusesUnsupportedRenameWithoutUsingTarget(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "new", "state.json")
	previous := renameStateDirectoryNoReplace
	t.Cleanup(func() { renameStateDirectoryNoReplace = previous })
	renameStateDirectoryNoReplace = func(string, string) error { return syscall.ENOTSUP }
	if err := firstUseStateEntry(t, "LockWriter", New(path)); !errors.Is(err, syscall.ENOTSUP) {
		t.Fatalf("unsupported publication accepted: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unsupported publication left a usable target or staging files: %v %v", entries, err)
	}
	// An already provisioned directory never requires the atomic creation primitive.
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := firstUseStateEntry(t, "LockWriter", New(path)); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryPublicationDoesNotReplaceCompetingDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "new")
	path := filepath.Join(dir, "state.json")
	previous := renameStateDirectoryNoReplace
	t.Cleanup(func() { renameStateDirectoryNoReplace = previous })
	var winner os.FileInfo
	renameStateDirectoryNoReplace = func(source, target string) error {
		if err := os.Mkdir(target, 0750); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(target, "existing"), []byte("retain"), 0600); err != nil {
			return err
		}
		var err error
		winner, err = os.Stat(target)
		if err != nil {
			return err
		}
		return previous(source, target)
	}
	if err := firstUseStateEntry(t, "LockWriter", New(path)); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(dir)
	data, readErr := os.ReadFile(filepath.Join(dir, "existing"))
	if err != nil || !os.SameFile(winner, after) || readErr != nil || string(data) != "retain" {
		t.Fatalf("competing directory replaced: info=%v err=%v data=%q read=%v", after, err, data, readErr)
	}
}

func TestDirectoryPublicationInspectsConflictOnce(t *testing.T) {
	for _, kind := range []string{"dangling symlink", "non-directory", "vanished"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			target := filepath.Join(parent, "new")
			path := filepath.Join(target, "state.json")
			previous := renameStateDirectoryNoReplace
			t.Cleanup(func() { renameStateDirectoryNoReplace = previous })
			attempts := 0
			renameStateDirectoryNoReplace = func(source, destination string) error {
				attempts++
				if attempts > 1 {
					return errors.New("bounded probe stopped repeated publication")
				}
				switch kind {
				case "dangling symlink":
					if err := os.Symlink(filepath.Join(parent, "missing"), destination); err != nil {
						return err
					}
				case "non-directory":
					if err := os.WriteFile(destination, []byte("existing contents"), 0600); err != nil {
						return err
					}
				case "vanished":
					// Model a competing creator removing its entry after EEXIST.
					return os.ErrExist
				}
				return previous(source, destination)
			}
			err := firstUseStateEntry(t, "LockWriter", New(path))
			if err == nil || attempts != 1 {
				t.Fatalf("conflict retried instead of inspecting once: attempts=%d err=%v", attempts, err)
			}
			if kind == "non-directory" {
				data, readErr := os.ReadFile(target)
				if !errors.Is(err, syscall.ENOTDIR) || readErr != nil || string(data) != "existing contents" {
					t.Fatalf("non-directory changed or unclear error: %q %v %v", data, readErr, err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unavailable destination lost its cause: %v", err)
			}
			if kind == "dangling symlink" {
				link, readErr := os.Readlink(target)
				if readErr != nil || link != filepath.Join(parent, "missing") {
					t.Fatalf("dangling link changed: %q %v", link, readErr)
				}
			}
			entries, readErr := os.ReadDir(parent)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".state-directory-") {
					t.Fatalf("conflict leaked staging directory: %s", entry.Name())
				}
			}
			if kind == "vanished" && len(entries) != 0 {
				t.Fatalf("vanished conflict created state: %v", entries)
			}
		})
	}
}
