// Package sshconfig reads and writes ~/.ssh/config without taking ownership of
// it.
//
// OpenSSH's config is the source of truth (ADR 0004): people edit it by hand,
// keep comments in it, and expect `ssh`, `scp` and everything else to keep
// working. So this package is deliberately surgical — it parses line ranges,
// rewrites only the keys it manages for the block being edited, and appends new
// blocks at the end. Everything it does not understand is preserved byte for
// byte, and a test asserts that with a realistic file.
package sshconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// managedKeys are the directives this package will write. Everything else is
// preserved and never reordered.
var managedKeys = []string{"HostName", "User", "Port", "IdentityFile", "ProxyJump"}

// Spec is what a host block is made of, as far as omssh is concerned.
type Spec struct {
	Alias        string
	HostName     string
	User         string
	Port         int
	IdentityFile string
	ProxyJump    string
}

// Block is one Host block in a file.
type Block struct {
	// Patterns are the arguments of the Host line, as written.
	Patterns []string
	// Start and End delimit the block's lines in File.Lines; End is exclusive.
	Start, End int
	// Source is the file the block came from.
	Source string
	// Fields holds the first value of each directive in the block, keyed by the
	// canonical capitalisation (e.g. "Hostname" folds into "HostName").
	Fields map[string]string
}

// Alias returns the first pattern, which is the name people type.
func (b Block) Alias() string {
	if len(b.Patterns) == 0 {
		return ""
	}
	return b.Patterns[0]
}

// Wildcard reports whether the block is a pattern block rather than a host.
func (b Block) Wildcard() bool {
	for _, p := range b.Patterns {
		if strings.ContainsAny(p, "*?!") || strings.HasPrefix(p, "!") {
			return true
		}
	}
	return false
}

// File is a parsed config file, loaded into memory so an edit is all-or-nothing.
type File struct {
	Path string
	// Lines are the file's lines, without terminators.
	Lines []string
	// Blocks are the Host blocks owned by this file (not its includes).
	Blocks []Block
	// Includes are the Include directives found at the top level.
	Includes []string
	// mode is the file's permission bits, preserved on save.
	mode os.FileMode
	// newline is "\n" or "\r\n", preserved on save.
	newline string
}

// Load parses path. A missing file is not an error: it returns an empty File
// that can be added to and saved, which is what a first-run "add a host" needs.
func Load(path string) (*File, error) {
	f := &File{Path: path, mode: 0o600, newline: "\n"}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if info, err := os.Stat(path); err == nil {
		f.mode = info.Mode().Perm()
	}
	text := string(data)
	if strings.Contains(text, "\r\n") {
		f.newline = "\r\n"
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	// A trailing newline should not produce a phantom empty line on save.
	f.Lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	f.parse()
	return f, nil
}

// parse walks the lines and fills Blocks and Includes.
func (f *File) parse() {
	inBlock, inMatch := false, false
	var current Block

	closeBlock := func(at int) {
		if !inBlock {
			return
		}
		current.End = at
		f.Blocks = append(f.Blocks, current)
		inBlock = false
	}

	for i, raw := range f.Lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value := splitDirective(line)
		switch strings.ToLower(key) {
		case "host":
			closeBlock(i)
			inMatch = false
			inBlock = true
			current = Block{
				Patterns: strings.Fields(value),
				Start:    i,
				Source:   f.Path,
				Fields:   map[string]string{},
			}
			continue
		case "match":
			// Match blocks have their own conditional scope. They are preserved
			// untouched and treated as the end of the current Host block.
			closeBlock(i)
			inMatch = true
			continue
		case "include":
			if fresh := value; fresh != "" && !containsString(f.Includes, fresh) {
				f.Includes = append(f.Includes, fresh)
			}
			continue
		}
		if inBlock && !inMatch {
			if canonical, ok := canonicalKey(key); ok {
				if _, seen := current.Fields[canonical]; !seen {
					current.Fields[canonical] = value
				}
			}
		}
	}
	closeBlock(len(f.Lines))
}

// splitDirective splits "Key value" or "Key=value" into its parts.
func splitDirective(line string) (key, value string) {
	if i := strings.IndexAny(line, " \t="); i >= 0 {
		return line[:i], strings.TrimSpace(strings.TrimPrefix(line[i:], "="))
	}
	return line, ""
}

// canonicalKey folds the spelling of a directive into the one we write.
func canonicalKey(key string) (string, bool) {
	for _, managed := range managedKeys {
		if strings.EqualFold(key, managed) {
			return managed, true
		}
	}
	return "", false
}

// Hosts returns the concrete host aliases in the file, sorted, excluding
// wildcard blocks. Aliases that appear more than once are listed once.
func (f *File) Hosts() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(f.Blocks))
	for _, b := range f.Blocks {
		if b.Wildcard() {
			continue
		}
		for _, p := range b.Patterns {
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Lookup returns the block that defines alias.
func (f *File) Lookup(alias string) (Block, bool) {
	for _, b := range f.Blocks {
		for _, p := range b.Patterns {
			if p == alias {
				return b, true
			}
		}
	}
	return Block{}, false
}

// Spec returns the host's values as omssh writes them.
func (b Block) Spec() Spec {
	spec := Spec{Alias: b.Alias()}
	spec.HostName = b.Fields["HostName"]
	spec.User = b.Fields["User"]
	spec.IdentityFile = b.Fields["IdentityFile"]
	spec.ProxyJump = b.Fields["ProxyJump"]
	if port, err := strconv.Atoi(b.Fields["Port"]); err == nil {
		spec.Port = port
	}
	return spec
}

// Validate checks a host before it reaches the file. The rules are the ones
// ssh(1) would reject or silently misread, caught early with a plain message.
func (s Spec) Validate() error {
	s.Alias = strings.TrimSpace(s.Alias)
	if s.Alias == "" {
		return errors.New("host: a name is required")
	}
	if strings.ContainsAny(s.Alias, " \t*?!") {
		return fmt.Errorf("host %q: names cannot contain spaces or wildcards", s.Alias)
	}
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("port: %d is not a valid port", s.Port)
	}
	if strings.ContainsAny(s.User, " \t") {
		return fmt.Errorf("user %q: must not contain spaces", s.User)
	}
	return nil
}

// Add appends a new block for spec. It refuses to create a duplicate alias,
// because OpenSSH would silently use only the first one.
func (f *File) Add(spec Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if _, exists := f.Lookup(spec.Alias); exists {
		return fmt.Errorf("host %q already exists", spec.Alias)
	}

	block := []string{"Host " + spec.Alias}
	for _, key := range managedKeys {
		if value := valueOf(spec, key); value != "" {
			block = append(block, "    "+key+" "+value)
		}
	}

	// One blank line between blocks, but no gratuitous blank at the end of a
	// file that never had one.
	if len(f.Lines) > 0 && strings.TrimSpace(f.Lines[len(f.Lines)-1]) != "" {
		f.Lines = append(f.Lines, "")
	}
	f.Lines = append(f.Lines, block...)
	f.parse()
	return nil
}

// Update rewrites the managed keys of an existing block, in place. Unknown
// directives, comments and ordering are preserved.
func (f *File) Update(original string, spec Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	block, ok := f.Lookup(original)
	if !ok {
		return fmt.Errorf("host %q is not in %s", original, f.Path)
	}
	if block.Wildcard() {
		return fmt.Errorf("host %q is a pattern block and is edited by hand", original)
	}

	// Renaming: the Host line is the first pattern, and extra patterns stay.
	hostLine := "Host " + spec.Alias
	if len(block.Patterns) > 1 {
		hostLine += " " + strings.Join(block.Patterns[1:], " ")
	}
	lines := make([]string, 0, block.End-block.Start+len(managedKeys))
	lines = append(lines, hostLine)

	written := map[string]bool{}
	for i := block.Start + 1; i < block.End; i++ {
		line := f.Lines[i]
		key, _ := splitDirective(strings.TrimSpace(line))
		canonical, managed := canonicalKey(key)
		if !managed {
			lines = append(lines, line)
			continue
		}
		if written[canonical] {
			continue // collapse duplicates into the one we wrote
		}
		written[canonical] = true
		if value := valueOf(spec, canonical); value != "" {
			lines = append(lines, "    "+canonical+" "+value)
		}
		// An empty value removes the directive: the same as not setting it.
	}
	for _, key := range managedKeys {
		if written[key] {
			continue
		}
		if value := valueOf(spec, key); value != "" {
			lines = append(lines, "    "+key+" "+value)
		}
	}

	f.Lines = append(append(append([]string{}, f.Lines[:block.Start]...), lines...), f.Lines[block.End:]...)
	f.parse()
	return nil
}

// Delete removes an existing block, and nothing else.
func (f *File) Delete(alias string) error {
	block, ok := f.Lookup(alias)
	if !ok {
		return fmt.Errorf("host %q is not in %s", alias, f.Path)
	}
	if block.Wildcard() {
		return fmt.Errorf("host %q is a pattern block and is edited by hand", alias)
	}
	start, end := block.Start, block.End
	// An Include inside the block's range is not part of the host: it pulls in
	// more configuration. Leave it where it is.
	for i := start + 1; i < end; i++ {
		if key, _ := splitDirective(strings.TrimSpace(f.Lines[i])); strings.EqualFold(key, "include") {
			end = i
			break
		}
	}
	// Take the blank separator with it, so deleting does not leave a gap.
	if end < len(f.Lines) && strings.TrimSpace(f.Lines[end]) == "" {
		end++
	}
	f.Lines = append(append([]string{}, f.Lines[:start]...), f.Lines[end:]...)
	f.parse()
	return nil
}

// Save writes the file atomically, creating ~/.ssh if it is missing.
func (f *File) Save() error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(f.Path), err)
	}
	text := strings.Join(f.Lines, f.newline)
	if text != "" && !strings.HasSuffix(text, f.newline) {
		text += f.newline
	}
	tmp := f.Path + ".omssh-tmp"
	if err := os.WriteFile(tmp, []byte(text), f.mode); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, f.Path); err != nil {
		return fmt.Errorf("replacing %s: %w", f.Path, err)
	}
	return nil
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// valueOf returns the text to write for a managed key, or "" to omit it.
func valueOf(spec Spec, key string) string {
	switch key {
	case "HostName":
		return strings.TrimSpace(spec.HostName)
	case "User":
		return strings.TrimSpace(spec.User)
	case "Port":
		if spec.Port == 0 {
			return ""
		}
		return strconv.Itoa(spec.Port)
	case "IdentityFile":
		return strings.TrimSpace(spec.IdentityFile)
	case "ProxyJump":
		return strings.TrimSpace(spec.ProxyJump)
	}
	return ""
}

// DefaultPath is ~/.ssh/config.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// LoadWithIncludes parses path and, read-only, the files it includes. Hosts from
// included files are listed with their own Source so editing can refuse them
// rather than silently writing to the wrong file.
func LoadWithIncludes(path string) ([]*File, error) {
	main, err := Load(path)
	if err != nil {
		return nil, err
	}
	files := []*File{main}
	seen := map[string]bool{path: true}

	var walk func(file *File, depth int) error
	walk = func(file *File, depth int) error {
		if depth > 4 {
			return nil
		}
		for _, pattern := range file.Includes {
			matches, err := includeMatches(pattern, filepath.Dir(file.Path))
			if err != nil {
				continue // an include we cannot expand is not fatal
			}
			for _, match := range matches {
				if seen[match] {
					continue
				}
				seen[match] = true
				included, err := Load(match)
				if err != nil {
					continue
				}
				files = append(files, included)
				if err := walk(included, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(main, 0); err != nil {
		return nil, err
	}
	return files, nil
}

// includeMatches expands an Include directive, which may be a glob and may be
// relative to the including file.
func includeMatches(pattern, dir string) ([]string, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, nil
	}
	if strings.HasPrefix(pattern, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		pattern = filepath.Join(home, pattern[2:])
	}
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(dir, pattern)
	}
	return filepath.Glob(pattern)
}
