package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// site builds a local tree and a remote tree, with every kind of difference:
// a new file, a changed file, an unchanged one, and a file only the remote has.
func site(t *testing.T) (local, remote *vfs.Local, localDir, remoteDir string) {
	t.Helper()
	localDir, remoteDir = t.TempDir(), t.TempDir()
	write := func(root, name, body string) {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(localDir, "index.html", "<html>new")
	write(localDir, "css/site.css", "body{}")
	write(localDir, "same.txt", "same")
	write(remoteDir, "index.html", "<html>old")
	write(remoteDir, "same.txt", "same")
	write(remoteDir, "stale.txt", "old page")
	write(remoteDir, "node_modules/dep.js", "dep")

	// The remote copy is older than the local one, which is what a plan compares:
	// size first, then modification time with a second of slack for filesystems
	// that keep less precision than the local disk.
	old := time.Now().Add(-time.Hour)
	for _, name := range []string{"index.html", "same.txt", "stale.txt"} {
		if err := os.Chtimes(filepath.Join(remoteDir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	// same.txt is identical on both sides, timestamp included: nothing to do.
	if err := os.Chtimes(filepath.Join(localDir, "same.txt"), old, old); err != nil {
		t.Fatal(err)
	}

	var err error
	local, err = vfs.NewLocal(localDir)
	if err != nil {
		t.Fatal(err)
	}
	remote, err = vfs.NewLocal(remoteDir)
	if err != nil {
		t.Fatal(err)
	}
	return local, remote, localDir, remoteDir
}

func TestPlanNamesEveryDifference(t *testing.T) {
	local, remote, _, _ := site(t)
	plan, err := Diff(context.Background(), local, remote, Options{LocalRoot: "/", RemoteRoot: "/"})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]Kind{}
	for _, change := range plan.Changes {
		kinds[change.Path] = change.Kind
	}
	if kinds["index.html"] != Update {
		t.Errorf("index.html should be an update, got %v", kinds["index.html"])
	}
	if kinds["css/site.css"] != Create {
		t.Errorf("css/site.css should be created, got %v", kinds["css/site.css"])
	}
	if kinds["same.txt"] != Same {
		t.Errorf("same.txt should be unchanged, got %v", kinds["same.txt"])
	}
	if _, ok := kinds["stale.txt"]; ok {
		t.Error("an overlay deploy should not mention remote-only files")
	}
	create, update, _ := plan.Counts()
	if create != 1 || update != 1 {
		t.Errorf("counts = %d create, %d update", create, update)
	}
	if plan.Bytes == 0 {
		t.Error("the plan should say how many bytes will move")
	}
}

// Sync mode is a mirror: what the local tree does not have is removed, except
// what the exclusions protect.
func TestSyncPlanDeletesAndRespectsExcludes(t *testing.T) {
	local, remote, _, _ := site(t)
	opts := Options{LocalRoot: "/", RemoteRoot: "/", Delete: true, Exclude: []string{"node_modules"}}
	plan, err := Diff(context.Background(), local, remote, opts)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Kind{}
	for _, change := range plan.Changes {
		found[change.Path] = change.Kind
	}
	if found["stale.txt"] != Delete {
		t.Errorf("stale.txt should be deleted in sync mode, got %v", found["stale.txt"])
	}
	if _, ok := found["node_modules/dep.js"]; ok {
		t.Error("an excluded file must not be deleted either")
	}
}

// Applying the plan writes the new bytes and, in sync mode, removes the stale
// ones.
func TestApplyWritesAndRemoves(t *testing.T) {
	local, remote, _, remoteDir := site(t)
	opts := Options{LocalRoot: "/", RemoteRoot: "/", Delete: true}
	if _, err := Apply(context.Background(), local, remote, opts, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(remoteDir, "index.html")); string(got) != "<html>new" {
		t.Errorf("index.html should hold the local bytes, got %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(remoteDir, "css", "site.css")); err != nil || string(got) != "body{}" {
		t.Errorf("the new file should be there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(remoteDir, "stale.txt")); !os.IsNotExist(err) {
		t.Error("sync mode should have removed stale.txt")
	}

	// A second run has nothing to do, which is what makes a deploy safe to
	// repeat.
	plan, err := Diff(context.Background(), local, remote, opts)
	if err != nil {
		t.Fatal(err)
	}
	if create, update, del := plan.Counts(); create+update+del != 0 {
		t.Errorf("a repeated deploy should be a no-op, got %d/%d/%d", create, update, del)
	}
}
