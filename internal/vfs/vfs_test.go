package vfs_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fakihariefnoto/omssh/internal/vfs"
	"github.com/fakihariefnoto/omssh/internal/vfs/vfstest"
)

// The conformance suite is the contract: Local must satisfy every assertion, and
// the SFTP implementation will have to satisfy the same ones when it lands.
func TestLocalConformance(t *testing.T) {
	fsys, err := vfs.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	vfstest.Run(t, fsys)
}

// A rooted filesystem must not reach outside its root, even through a path that
// climbs. Clean clamps it, and the real path is checked again afterwards.
func TestLocalConfinesToItsRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file outside the root, which must stay untouched.
	outside := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	fsys, err := vfs.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}

	// Reading through a climbing path finds nothing outside.
	if _, err := vfs.ReadAll(fsys, "/../outside.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reading outside the root = %v, want ErrNotExist", err)
	}
	if got, err := vfs.ReadAll(fsys, "../outside.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reading outside the root = %v (content %q), want ErrNotExist", err, got)
	}

	// Writing through a climbing path lands inside, not outside.
	if err := vfs.WriteFile(fsys, "/../escaped.txt", []byte("x"), 0o600); err != nil {
		t.Fatalf("writing through a climbing path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "escaped.txt")); err == nil {
		t.Fatal("a file escaped the root")
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.txt")); err != nil {
		t.Errorf("the file should be inside the root: %v", err)
	}
	// And the outside file is exactly as it was.
	if data, err := os.ReadFile(outside); err != nil || string(data) != "secret" {
		t.Errorf("the file outside the root changed: %q %v", data, err)
	}

	// RemoveAll through a climbing path must not delete outside the root.
	if err := fsys.RemoveAll("/.."); err == nil {
		t.Error("RemoveAll(..) should be refused")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("something outside the root was removed: %v", err)
	}
}

// Abs is what logs and error messages use, so it must produce a real path.
func TestLocalAbsAndString(t *testing.T) {
	root := t.TempDir()
	fsys, err := vfs.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := fsys.Abs("/a/b")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	if !strings.HasPrefix(abs, root) || !strings.HasSuffix(abs, filepath.Join("a", "b")) {
		t.Errorf("Abs = %q, want a path under %q", abs, root)
	}
	if fsys.String() != root {
		t.Errorf("String = %q, want the root", fsys.String())
	}
}

// A root that does not exist is a programming error worth catching at
// construction, not a surprise on the first listing.
func TestNewLocalRejectsMissingRoot(t *testing.T) {
	if _, err := vfs.NewLocal(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("NewLocal should refuse a missing directory")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := vfs.NewLocal(file); err == nil {
		t.Error("NewLocal should refuse a file as a root")
	}
}
