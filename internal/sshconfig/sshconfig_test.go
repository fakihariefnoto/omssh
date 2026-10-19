package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `# Personal machines
Host *
    ServerAliveInterval 30
    AddKeysToAgent yes

Host prod-api
    HostName 203.0.113.10
    User deploy
    Port 2222
    IdentityFile ~/.ssh/id_ed25519
    # keep this comment
    ForwardAgent no

Host staging staging.internal
    HostName staging.example.com
    User ubuntu
    ProxyJump prod-api

Include ~/.ssh/config.d/*

Match host *.internal
    User admin
`

func writeSample(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadSample(t *testing.T) (*File, string) {
	t.Helper()
	path := writeSample(t)
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f, path
}

// Wildcard and Match blocks are part of the file but are not hosts you can open.
func TestHostsExcludesPatterns(t *testing.T) {
	f, _ := loadSample(t)
	got := f.Hosts()
	want := []string{"prod-api", "staging", "staging.internal"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Hosts() = %v, want %v", got, want)
	}
}

func TestLookupAndSpec(t *testing.T) {
	f, _ := loadSample(t)
	block, ok := f.Lookup("prod-api")
	if !ok {
		t.Fatal("prod-api should be found")
	}
	spec := block.Spec()
	if spec.HostName != "203.0.113.10" || spec.User != "deploy" || spec.Port != 2222 {
		t.Errorf("unexpected spec: %+v", spec)
	}
	if spec.IdentityFile != "~/.ssh/id_ed25519" {
		t.Errorf("identity not parsed: %+v", spec)
	}
	if block.Wildcard() {
		t.Error("prod-api is not a wildcard")
	}

	global, ok := f.Lookup("*")
	if !ok {
		t.Fatal("the Host * block should be found")
	}
	if !global.Wildcard() {
		t.Error("Host * should be reported as a wildcard")
	}
	if global.Fields["ServerAliveInterval"] != "" {
		t.Error("unmanaged directives should not be in Fields")
	}
}

// Adding a host must not disturb a single byte of what was already there.
func TestAddPreservesTheFile(t *testing.T) {
	f, path := loadSample(t)
	if err := f.Add(Spec{Alias: "db", HostName: "db.example.com", User: "postgres", Port: 5432}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(after)
	if !strings.HasPrefix(text, sample) {
		t.Errorf("the original file was modified:\n%s", text)
	}
	if !strings.Contains(text, "Host db\n    HostName db.example.com\n    User postgres\n    Port 5432") {
		t.Errorf("the new block is missing or malformed:\n%s", text)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Lookup("db"); !ok {
		t.Error("the added host does not parse back")
	}
}

// Updating rewrites the managed keys in place and leaves comments, unknown
// directives and the rest of the file alone.
func TestUpdateIsSurgical(t *testing.T) {
	f, path := loadSample(t)
	block, _ := f.Lookup("prod-api")
	spec := block.Spec()
	spec.User = "root"
	spec.Port = 0 // remove the Port directive
	spec.HostName = "new.example.com"

	if err := f.Update("prod-api", spec); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}

	text := readFile(t, path)
	if !strings.Contains(text, "Host prod-api\n    HostName new.example.com\n    User root") {
		t.Errorf("the header of the block is wrong:\n%s", text)
	}
	if strings.Contains(text, "Port 2222") {
		t.Errorf("clearing the port should remove the directive:\n%s", text)
	}
	if !strings.Contains(text, "# keep this comment") || !strings.Contains(text, "ForwardAgent no") {
		t.Errorf("unmanaged lines were lost:\n%s", text)
	}
	if !strings.Contains(text, "IdentityFile ~/.ssh/id_ed25519") {
		t.Errorf("an unchanged managed key was lost:\n%s", text)
	}
	// The other hosts must be untouched.
	if !strings.Contains(text, "Host staging staging.internal\n    HostName staging.example.com") {
		t.Errorf("a neighbouring block was disturbed:\n%s", text)
	}
}

// Renaming keeps extra patterns, so a block with aliases keeps all of them.
func TestUpdateRenamesAndKeepsExtraPatterns(t *testing.T) {
	f, path := loadSample(t)
	block, _ := f.Lookup("staging")
	spec := block.Spec()
	spec.Alias = "stg"

	if err := f.Update("staging", spec); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if !strings.Contains(text, "Host stg staging.internal") {
		t.Errorf("the extra alias was dropped:\n%s", text)
	}
}

// Deleting removes the block and its separator, and nothing else.
func TestDeleteRemovesOnlyThatBlock(t *testing.T) {
	f, path := loadSample(t)
	if err := f.Delete("prod-api"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if strings.Contains(text, "prod-api") && !strings.Contains(text, "ProxyJump prod-api") {
		t.Errorf("the block was not removed:\n%s", text)
	}
	if !strings.Contains(text, "Host staging staging.internal") {
		t.Errorf("the next block was removed too:\n%s", text)
	}
	if !strings.Contains(text, "Host *") || !strings.Contains(text, "ServerAliveInterval 30") {
		t.Errorf("the global block was disturbed:\n%s", text)
	}
}

func TestDeleteRefusesPatternBlocks(t *testing.T) {
	f, _ := loadSample(t)
	if err := f.Delete("*"); err == nil {
		t.Error("deleting Host * should be refused: it is edited by hand")
	}
}

func TestAddRefusesDuplicates(t *testing.T) {
	f, _ := loadSample(t)
	err := f.Add(Spec{Alias: "prod-api", HostName: "other.example.com"})
	if err == nil {
		t.Fatal("adding an existing alias should be refused")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("the error should say why: %v", err)
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]Spec{
		"no name":     {},
		"spaces":      {Alias: "my host"},
		"wildcard":    {Alias: "*"},
		"bad port":    {Alias: "a", Port: 70000},
		"spacey user": {Alias: "a", User: "two words"},
	}
	for name, spec := range cases {
		if err := spec.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := (Spec{Alias: "a", Port: 22}).Validate(); err != nil {
		t.Errorf("a valid spec was rejected: %v", err)
	}
}

// A missing file is not an error: a first run has to be able to create one.
func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "config")
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Hosts()) != 0 {
		t.Error("a missing file should have no hosts")
	}
	if err := f.Add(Spec{Alias: "first", HostName: "example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("a new config should be 0600, got %v", info.Mode().Perm())
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Hosts(); len(got) != 1 || got[0] != "first" {
		t.Errorf("Hosts() = %v", got)
	}
}

// CRLF files stay CRLF: a config shared with a Windows editor must not be
// rewritten underneath its owner.
func TestCRLFIsPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("Host a\r\n    HostName a.example.com\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add(Spec{Alias: "b", HostName: "b.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if strings.Count(text, "\n") != strings.Count(text, "\r\n") {
		t.Errorf("some line endings are not CRLF:\n%q", text)
	}
	if !strings.HasPrefix(text, "Host a\r\n    HostName a.example.com\r\n") {
		t.Errorf("the original content was rewritten:\n%q", text)
	}
	if !strings.Contains(text, "Host b\r\n    HostName b.example.com\r\n") {
		t.Errorf("the new block is missing:\n%q", text)
	}
}

func TestIncludeDirectivesAreRecorded(t *testing.T) {
	f, _ := loadSample(t)
	if len(f.Includes) != 1 || f.Includes[0] != "~/.ssh/config.d/*" {
		t.Errorf("Includes = %v", f.Includes)
	}

	// A host in an included file is listed with its own source, so the editor can
	// refuse to write it into the wrong file.
	dir := t.TempDir()
	main := filepath.Join(dir, "config")
	dropin := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(dropin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte("Host local\n    HostName local.example.com\nInclude "+dropin+"/*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropin, "extra"), []byte("Host fromdropin\n    HostName drop.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := LoadWithIncludes(main)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected the main file and one include, got %d", len(files))
	}
	if got := files[1].Hosts(); len(got) != 1 || got[0] != "fromdropin" {
		t.Errorf("included hosts = %v", got)
	}
	// And the main file cannot edit a host it does not own.
	if err := files[0].Update("fromdropin", Spec{Alias: "fromdropin"}); err == nil {
		t.Error("editing a host from an include through the main file should fail")
	}
}

func TestUnknownDirectivesAndOrderingSurvive(t *testing.T) {
	f, path := loadSample(t)
	before := readFile(t, path)

	block, _ := f.Lookup("staging")
	spec := block.Spec()
	spec.User = "deploy"
	if err := f.Update("staging", spec); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	after := readFile(t, path)

	// The only difference is the user line.
	lines := strings.Split(after, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "User deploy") {
			found = true
		}
	}
	if !found {
		t.Error("the edited user is missing")
	}
	for _, keep := range []string{"# Personal machines", "AddKeysToAgent yes", "Match host *.internal", "User admin"} {
		if !strings.Contains(after, keep) {
			t.Errorf("%q was lost", keep)
		}
	}
	// The number of lines is unchanged: an update must not grow the file.
	if len(strings.Split(before, "\n")) != len(lines) {
		t.Errorf("line count changed from %d to %d", len(strings.Split(before, "\n")), len(lines))
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
