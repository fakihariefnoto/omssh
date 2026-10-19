// Package vfstest is the conformance suite every vfs.FS implementation must pass.
//
// The interface is the widest abstraction in the codebase (ADR 0001), so it is
// the one worth pinning hardest: if Local, Mem and SFTP all satisfy the same
// assertions, then copy, extract, compress and deploy — written once against the
// interface — behave identically in all four directions.
package vfstest

import (
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// Run exercises fsys. Each subtest starts from the same empty root, so a caller
// should hand in a fresh filesystem.
func Run(t *testing.T, fsys vfs.FS) {
	t.Helper()
	caps := fsys.Capabilities()

	t.Run("CapabilitiesAreCoherent", func(t *testing.T) {
		if caps.Symlink {
			if err := fsys.Symlink("target", "/cap-link"); err != nil {
				t.Errorf("Symlink is advertised but failed: %v", err)
			}
		} else if err := fsys.Symlink("target", "/cap-link"); !errors.Is(err, vfs.ErrUnsupported) {
			t.Errorf("Symlink without the capability should report ErrUnsupported, got %v", err)
		}
		if caps.Chmod {
			if err := vfs.WriteFile(fsys, "/cap-mode", []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := fsys.Chmod("/cap-mode", 0o640); err != nil {
				t.Errorf("Chmod is advertised but failed: %v", err)
			}
		}
		if caps.Chtimes {
			if err := vfs.WriteFile(fsys, "/cap-time", []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
			if err := fsys.Chtimes("/cap-time", when, when); err != nil {
				t.Errorf("Chtimes is advertised but failed: %v", err)
			}
		}
	})

	t.Run("WriteReadRoundTrip", func(t *testing.T) {
		body := "line one\nline two\n"
		if err := vfs.WriteFile(fsys, "/file.txt", []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		got, err := vfs.ReadAll(fsys, "/file.txt")
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if string(got) != body {
			t.Errorf("read %q, want %q", got, body)
		}
		// io/fs helpers must work too: the interface promises to be close to it.
		viaFS, err := fs.ReadFile(fsys, "file.txt")
		if err != nil {
			t.Fatalf("fs.ReadFile with a relative name: %v", err)
		}
		if string(viaFS) != body {
			t.Errorf("fs.ReadFile read %q", viaFS)
		}
	})

	t.Run("StatDescribesTheFile", func(t *testing.T) {
		if err := vfs.WriteFile(fsys, "/described", []byte("12345"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := fsys.Stat("/described")
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.IsDir() {
			t.Error("a file should not be a directory")
		}
		if info.Size() != 5 {
			t.Errorf("size is %d, want 5", info.Size())
		}
		if info.Name() != "described" {
			t.Errorf("name is %q, want the base name", info.Name())
		}
		if info.ModTime().IsZero() {
			t.Error("a modification time should be set")
		}
	})

	t.Run("MkdirAndReadDirSorted", func(t *testing.T) {
		if err := fsys.Mkdir("/dir", 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		for _, name := range []string{"c.txt", "a.txt", "b.txt"} {
			if err := vfs.WriteFile(fsys, fsys.Join("/dir", name), []byte(name), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		entries, err := fsys.ReadDir("/dir")
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if len(entries) != 3 {
			t.Fatalf("ReadDir returned %d entries, want 3", len(entries))
		}
		if !sort.SliceIsSorted(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() }) {
			t.Errorf("ReadDir must be sorted by name, got %q %q %q", entries[0].Name(), entries[1].Name(), entries[2].Name())
		}
		// Stat on a directory must say so: the file manager renders it differently.
		info, err := fsys.Stat("/dir")
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Error("a directory should report IsDir")
		}
	})

	t.Run("MkdirAllCreatesParents", func(t *testing.T) {
		if err := fsys.MkdirAll("/a/b/c", 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		for _, dir := range []string{"/a", "/a/b", "/a/b/c"} {
			info, err := fsys.Stat(dir)
			if err != nil {
				t.Fatalf("Stat(%s): %v", dir, err)
			}
			if !info.IsDir() {
				t.Errorf("%s is not a directory", dir)
			}
		}
	})

	t.Run("MkdirOnExistingFails", func(t *testing.T) {
		if err := fsys.Mkdir("/once", 0o755); err != nil {
			t.Fatal(err)
		}
		// The interface promises that this fails, not which error it is: servers
		// differ, and both OpenSSH's sftp-server and the library used here report
		// a generic failure rather than "already exists". An implementation that
		// can map it precisely may, and callers must not depend on the sentinel.
		if err := fsys.Mkdir("/once", 0o755); err == nil {
			t.Error("Mkdir over an existing directory should fail")
		}
	})

	t.Run("CreateTruncates", func(t *testing.T) {
		if err := vfs.WriteFile(fsys, "/trunc", []byte("a long body"), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := fsys.Create("/trunc")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := io.WriteString(file, "short"); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		got, err := vfs.ReadAll(fsys, "/trunc")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "short" {
			t.Errorf("after Create the file holds %q, want %q", got, "short")
		}
	})

	t.Run("OpenFileExclusive", func(t *testing.T) {
		if err := vfs.WriteFile(fsys, "/only", []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		// As above: O_EXCL must refuse, but the sentinel is not promised on a
		// protocol that often answers with a generic failure.
		if _, err := fsys.OpenFile("/only", vfs.O_WRONLY|vfs.O_CREATE|vfs.O_EXCL, 0o600); err == nil {
			t.Error("O_EXCL over an existing file should fail")
		}
		file, err := fsys.OpenFile("/fresh", vfs.O_WRONLY|vfs.O_CREATE|vfs.O_EXCL, 0o600)
		if err != nil {
			t.Fatalf("O_EXCL on a new file: %v", err)
		}
		_ = file.Close()
	})

	t.Run("MissingPathsReportNotExist", func(t *testing.T) {
		if _, err := fsys.Open("/nope"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Open = %v, want ErrNotExist", err)
		}
		if _, err := fsys.Stat("/nope"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat = %v, want ErrNotExist", err)
		}
		if _, err := fsys.ReadDir("/nope"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadDir = %v, want ErrNotExist", err)
		}
		if _, err := vfs.ReadAll(fsys, "/nope"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadAll = %v, want ErrNotExist", err)
		}
	})

	t.Run("MissingParentIsReported", func(t *testing.T) {
		_, err := fsys.OpenFile("/no/such/dir/file", vfs.O_WRONLY|vfs.O_CREATE, 0o600)
		if err == nil {
			t.Fatal("creating a file under a missing directory should fail")
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("got %v, want ErrNotExist", err)
		}
	})

	t.Run("RenameMoves", func(t *testing.T) {
		if err := vfs.WriteFile(fsys, "/from", []byte("body"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fsys.Rename("/from", "/to"); err != nil {
			t.Fatalf("Rename: %v", err)
		}
		if _, err := fsys.Stat("/from"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the source should be gone, got %v", err)
		}
		got, err := vfs.ReadAll(fsys, "/to")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "body" {
			t.Errorf("the destination holds %q", got)
		}
	})

	t.Run("RenameReplacesTheDestination", func(t *testing.T) {
		if err := vfs.WriteFile(fsys, "/r-from", []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := vfs.WriteFile(fsys, "/r-to", []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fsys.Rename("/r-from", "/r-to"); err != nil {
			t.Fatalf("Rename over an existing file: %v", err)
		}
		got, _ := vfs.ReadAll(fsys, "/r-to")
		if string(got) != "new" {
			t.Errorf("the destination holds %q, want the moved file's contents", got)
		}
	})

	t.Run("RemoveAndRemoveAll", func(t *testing.T) {
		if err := vfs.WriteFile(fsys, "/gone", []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fsys.Remove("/gone"); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, err := fsys.Stat("/gone"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the file should be gone, got %v", err)
		}
		if err := fsys.Remove("/gone"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("removing a missing file = %v, want ErrNotExist", err)
		}

		if err := fsys.MkdirAll("/tree/deep", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := vfs.WriteFile(fsys, "/tree/deep/leaf", []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fsys.RemoveAll("/tree"); err != nil {
			t.Fatalf("RemoveAll: %v", err)
		}
		if _, err := fsys.Stat("/tree"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the tree should be gone, got %v", err)
		}
		// RemoveAll on something that is already gone is not an error, which is
		// what makes it safe to call after a partial failure.
		if err := fsys.RemoveAll("/tree"); err != nil {
			t.Errorf("RemoveAll on a missing path = %v, want nil", err)
		}
	})

	t.Run("RootIsNeverRemoved", func(t *testing.T) {
		if err := fsys.RemoveAll("/"); err == nil {
			t.Error("RemoveAll on the root must be refused")
		}
		if err := fsys.Remove("/"); err == nil {
			t.Error("Remove on the root must be refused")
		}
	})

	t.Run("SymlinkIsListedAsALink", func(t *testing.T) {
		if !caps.Symlink {
			t.Skip("this filesystem has no symlinks")
		}
		if err := vfs.WriteFile(fsys, "/target", []byte("body"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fsys.Symlink("target", "/link"); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		target, err := fsys.Readlink("/link")
		if err != nil {
			t.Fatalf("Readlink: %v", err)
		}
		if target != "target" {
			t.Errorf("Readlink = %q, want %q", target, "target")
		}
		// Stat follows, Lstat does not: the file manager depends on the
		// difference.
		followed, err := fsys.Stat("/link")
		if err != nil {
			t.Fatalf("Stat on a symlink: %v", err)
		}
		if followed.IsDir() || followed.Size() != 4 {
			t.Errorf("Stat should describe the target, got size %d", followed.Size())
		}
		link, err := fsys.Lstat("/link")
		if err != nil {
			t.Fatalf("Lstat: %v", err)
		}
		if link.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("Lstat should report a symlink, got mode %v", link.Mode())
		}
	})

	t.Run("TruncateResizes", func(t *testing.T) {
		if err := vfs.WriteFile(fsys, "/cut", []byte("1234567890"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fsys.Truncate("/cut", 4); err != nil {
			t.Fatalf("Truncate: %v", err)
		}
		got, _ := vfs.ReadAll(fsys, "/cut")
		if string(got) != "1234" {
			t.Errorf("after truncating to 4 the file holds %q", got)
		}
	})

	t.Run("PathHelpersAgree", func(t *testing.T) {
		if got := fsys.Join("/dir", "sub", "file"); got != "/dir/sub/file" {
			t.Errorf("Join = %q", got)
		}
		if got := fsys.Dir("dir/sub/file"); got != "dir/sub" {
			t.Errorf("Dir = %q", got)
		}
		if got := fsys.Base("dir/sub/file"); got != "file" {
			t.Errorf("Base = %q", got)
		}
		if _, err := fsys.Abs("dir/file"); err != nil {
			t.Errorf("Abs: %v", err)
		}
		if fsys.String() == "" {
			t.Error("String should name the filesystem for logs")
		}
	})

	t.Run("NamesAreCleanedAndConfined", func(t *testing.T) {
		// ".." resolves rather than escaping, and a clean name is slash-rooted.
		if err := vfs.WriteFile(fsys, "/a/../b.txt", []byte("body"), 0o600); err != nil {
			t.Fatalf("writing through a relative segment: %v", err)
		}
		if _, err := fsys.Stat("b.txt"); err != nil {
			t.Errorf("the file should have landed at the root: %v", err)
		}
		if _, err := fsys.Stat("/../b.txt"); err != nil {
			t.Errorf("a climbing path should clamp to the root, got %v", err)
		}
		if err := fsys.MkdirAll("/deep/deeper", 0o755); err != nil {
			t.Fatal(err)
		}
		// Going up from a subdirectory lands inside the root, not outside it.
		if err := vfs.WriteFile(fsys, "/deep/../../clamped.txt", []byte("x"), 0o600); err != nil {
			t.Fatalf("writing through two relative segments: %v", err)
		}
		if _, err := fsys.Stat("/clamped.txt"); err != nil {
			t.Errorf("the clamped path should be at the root: %v", err)
		}
	})
}

// TestCleanClampsToTheRoot checks the pure path function every implementation
// relies on, so the confinement rule is tested on its own terms.
func TestCleanClampsToTheRoot(t *testing.T) {
	cases := map[string]string{
		"":                  "/",
		"/":                 "/",
		".":                 "/",
		"a":                 "/a",
		"/a":                "/a",
		"a/b":               "/a/b",
		"/a/b/":             "/a/b",
		"a/./b":             "/a/b",
		"a/../b":            "/b",
		"../a":              "/a",
		"../../../../etc/p": "/etc/p",
		"/a/b/../..":        "/",
	}
	for in, want := range cases {
		if got := vfs.Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"..", "../..", "/../../..", "a/../../.."} {
		if got := vfs.Clean(in); strings.Contains(got, "..") {
			t.Errorf("Clean(%q) = %q, which still contains ..", in, got)
		}
	}
}
