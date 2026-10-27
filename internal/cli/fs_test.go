package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fakihariefnoto/omssh/internal/config"
)

// The place grammar is one colon: a name on the left, an absolute path on the
// right, and a bare name means the root.
func TestSplitSpec(t *testing.T) {
	cases := []struct {
		spec  string
		place string
		path  string
	}{
		{"prod:/srv/app", "prod", "/srv/app"},
		{"prod:/", "prod", "/"},
		{"prod", "prod", "/"},
		{"local:", "local", "/"},
		{":/tmp/x", "local", "/tmp/x"},
		{"site:/var/www/./app/", "site", "/var/www/app"},
	}
	for _, tc := range cases {
		place, path := splitSpec(tc.spec)
		if place != tc.place || path != tc.path {
			t.Errorf("splitSpec(%q) = %q, %q; want %q, %q", tc.spec, place, path, tc.place, tc.path)
		}
	}
}

// The local place exercises the whole grammar without a network: put, stat, get
// and rm, in both the human and the JSON shape.
func TestFsLocalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	source := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(source, []byte("hello agent"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, string, int) {
		var stdout, stderr bytes.Buffer
		code := Execute(append(args, "--config", filepath.Join(dir, "config.yaml")), &stdout, &stderr)
		return stdout.String(), stderr.String(), code
	}

	remote := "local:" + filepath.Join(dir, "sub", "file.txt")
	if out, errOut, code := run("fs", "put", source, remote, "--json", "--allow-write"); code != 0 {
		t.Fatalf("put failed: %d %s %s", code, out, errOut)
	}
	out, _, code := run("fs", "ls", "local:"+filepath.Join(dir, "sub"), "--json")
	if code != 0 {
		t.Fatalf("ls failed: %d %s", code, out)
	}
	var listing struct {
		Entries []struct {
			Name string `json:"name"`
			Dir  bool   `json:"dir"`
			Size int64  `json:"size"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("ls is not JSON: %v\n%s", err, out)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "file.txt" || listing.Entries[0].Size != 11 {
		t.Fatalf("unexpected listing: %+v", listing)
	}

	out, _, code = run("fs", "get", remote, "-")
	if code != 0 || out != "hello agent" {
		t.Fatalf("get = %q (code %d)", out, code)
	}

	if _, _, code := run("fs", "rm", remote, "--allow-write"); code != 0 {
		t.Fatalf("rm failed: %d", code)
	}
	if out, _, code := run("fs", "stat", remote, "--json"); code == 0 {
		t.Fatalf("stat of a removed file should fail:\n%s", out)
	}
}

// A place that is neither local nor a stored profile is an SSH host, and a host
// that cannot be reached is a failure rather than a hang.
func TestFsUnknownPlaceFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"fs", "ls", "no-such-host-xyz:/", "--batch",
		"--config", filepath.Join(t.TempDir(), "config.yaml")}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("an unreachable host should not exit 0: %s", stdout.String())
	}
	if strings.TrimSpace(stderr.String()) == "" {
		t.Error("the failure should say something on stderr")
	}
}

// The safety model: reads are free, writes need the run to have said so, and the
// allowlists narrow the places and paths before anything is opened.
func TestFsRefusesWithoutPermission(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	source := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.yaml")
	remote := "local:" + filepath.Join(dir, "out.txt")

	run := func(args ...string) (string, int) {
		var stdout, stderr bytes.Buffer
		code := Execute(append(append(args, "--config", cfg), "--batch"), &stdout, &stderr)
		return stderr.String(), code
	}

	// A write without --allow-write exits 77 and changes nothing.
	if msg, code := run("fs", "put", source, remote); code != exitPermission {
		t.Fatalf("put without --allow-write = %d, want %d (%s)", code, exitPermission, msg)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); err == nil {
		t.Fatal("a refused put must not create the file")
	}

	// With --allow-write but a narrower allowlist, the place and path still gate.
	if msg, code := run("fs", "put", source, remote, "--allow-write", "--allow-host", "prod"); code != exitPermission {
		t.Fatalf("a place outside --allow-host = %d, want %d (%s)", code, exitPermission, msg)
	}
	if msg, code := run("fs", "put", source, remote, "--allow-write", "--allow-path", "/srv/app"); code != exitPermission {
		t.Fatalf("a path outside --allow-path = %d, want %d (%s)", code, exitPermission, msg)
	}
	if msg, code := run("fs", "put", source, remote, "--allow-write", "--allow-host", "local", "--allow-path", dir); code != 0 {
		t.Fatalf("an allowed put failed: %d (%s)", code, msg)
	}

	// Every change leaves a line in the audit log.
	state, err := config.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(state, "audit.jsonl"))
	if err != nil {
		t.Fatalf("audit log: %v", err)
	}
	if !strings.Contains(string(body), `"action":"put"`) || !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("audit log should record the put:\n%s", body)
	}

	// A read needs no permission at all.
	if msg, code := run("fs", "ls", "local:"+dir); code != 0 {
		t.Errorf("ls should be free: %d (%s)", code, msg)
	}
}
