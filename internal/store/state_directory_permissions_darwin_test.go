package store

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestStateDirectoryPublicationPreservesInheritedDarwinPermissions(t *testing.T) {
	owner, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	group := os.Getegid()
	for _, candidate := range groups {
		if candidate != group {
			group = candidate
			break
		}
	}
	for _, entry := range []string{"LockWriter", "Save"} {
		t.Run(entry, func(t *testing.T) {
			parent := t.TempDir()
			if err := os.Chown(parent, -1, group); err != nil {
				t.Fatal(err)
			}
			acl := "user:" + owner.Username + " allow list,search,readattr,readsecurity,file_inherit,directory_inherit"
			if out, err := exec.Command("/bin/chmod", "+a", acl, parent).CombinedOutput(); err != nil {
				t.Fatalf("set temporary parent ACL: %v: %s", err, out)
			}
			ordinary := filepath.Join(parent, "ordinary")
			if err := os.Mkdir(ordinary, 0755); err != nil {
				t.Fatal(err)
			}
			published := filepath.Join(parent, "published")
			if err := firstUseStateEntry(t, entry, New(filepath.Join(published, "state.json"))); err != nil {
				t.Fatal(err)
			}
			want, err := os.Stat(ordinary)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.Stat(published)
			if err != nil {
				t.Fatal(err)
			}
			wantStat, gotStat := want.Sys().(*syscall.Stat_t), got.Sys().(*syscall.Stat_t)
			if got.Mode() != want.Mode() || gotStat.Uid != wantStat.Uid || gotStat.Gid != wantStat.Gid || int(gotStat.Gid) != group {
				t.Fatalf("published directory differs from ordinary mkdir0755: mode=%v/%v uid=%d/%d gid=%d/%d expected parent gid=%d", got.Mode(), want.Mode(), gotStat.Uid, wantStat.Uid, gotStat.Gid, wantStat.Gid, group)
			}
			readACL := func(path string) string {
				t.Helper()
				out, err := exec.Command("/bin/ls", "-lde", path).CombinedOutput()
				if err != nil {
					t.Fatalf("read temporary directory ACL: %v: %s", err, out)
				}
				_, entries, ok := strings.Cut(string(out), "\n")
				if !ok || !strings.Contains(entries, " inherited allow ") {
					t.Fatalf("fixture did not inherit an ACL: %s", out)
				}
				return entries
			}
			if wantACL, gotACL := readACL(ordinary), readACL(published); gotACL != wantACL {
				t.Fatalf("publication changed inherited ACL: got=%q want=%q", gotACL, wantACL)
			}
		})
	}
}
