package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/omssh/internal/cloud"
	"github.com/fakihariefnoto/omssh/internal/cloudcred"
	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/keys"
	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// One grammar for every place an agent may touch:
//
//	omssh fs ls prod:/srv/app
//	omssh fs get prod:/srv/app/dump.sql ./dump.sql
//	omssh fs put ./dist/index.html site:/var/www
//
// A place is "local", a cloud profile name, or an SSH host. The same verbs work on
// all three, because the interface underneath is one interface.

// exitCodeError carries the process status an agent branches on.
type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) Error() string { return e.err.Error() }
func (e *exitCodeError) Unwrap() error { return e.err }
func (e *exitCodeError) ExitCode() int { return e.code }

// Exit codes, documented in DOCUMENTATION.md: 65 is "the host is not trusted
// yet", 77 is "the credentials cannot do this", and both are the agent's cue to
// stop rather than retry.
const (
	exitUsage      = 64
	exitHostKey    = 65
	exitPermission = 77
)

func newFsCmd(opts *Options, stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fs",
		Short: "Read and write files on a host, a bucket or the local disk",
		Long: "One path grammar for every place: <place>:<path>, where a place is\n" +
			"\"local\", a cloud profile name, or an SSH host.\n\n" +
			"With --batch nothing prompts: an unknown host key exits 65 with the\n" +
			"fingerprint, and a password that is needed and not stored exits 77.",
	}
	cmd.AddCommand(newFsLsCmd(opts, stdout))
	cmd.AddCommand(newFsStatCmd(opts, stdout))
	cmd.AddCommand(newFsGetCmd(opts, stdout))
	cmd.AddCommand(newFsPutCmd(opts, stdout))
	cmd.AddCommand(newFsRmCmd(opts, stdout))
	cmd.AddCommand(newFsMkdirCmd(opts, stdout))
	return cmd
}

// place is one resolved target: a filesystem, the path inside it, and how to let
// it go.
type place struct {
	fsys vfs.FS
	path string
	done func()
}

// splitSpec reads "<place>:<path>". A bare place means its root.
func splitSpec(spec string) (placeName, path string) {
	var rest string
	var ok bool
	placeName, rest, ok = strings.Cut(spec, ":")
	if !ok {
		placeName = spec
		rest = "/"
	}
	if strings.TrimSpace(placeName) == "" {
		placeName = "local"
	}
	if rest == "" {
		rest = "/"
	}
	return placeName, vfs.Clean(rest)
}

// openPlace resolves a place name: local, a cloud profile, or an SSH host.
func openPlace(opts *Options, cfg config.Config, name string) (*place, error) {
	switch name {
	case "local", "localhost":
		return &place{fsys: mustLocalFS(), path: "/", done: func() {}}, nil
	}
	if profile, ok := cfg.Cloud.Profiles[name]; ok {
		id, secret, _ := cloudcred.Load(keys.Default("omssh"), name)
		fsys, err := cloud.Open(context.Background(), name, profile, cloud.Credentials{
			AccessKey: id, Secret: secret, Username: id, Password: secret,
		})
		if err != nil {
			return nil, classify(err)
		}
		return &place{fsys: fsys, path: "/", done: func() {}}, nil
	}

	// An SSH host: connect, use the session's filesystem, close on the way out.
	target, err := execTarget(name)
	if err != nil {
		return nil, classify(err)
	}
	manager, err := session.New(session.Options{Prompter: &cliPrompter{in: os.Stdin, err: opts.stderr(), batch: opts.Batch}})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	live, err := manager.Connect(ctx, target)
	if err != nil {
		_ = manager.CloseAll()
		return nil, classify(err)
	}
	return &place{fsys: live.FS, path: "/", done: func() { _ = manager.CloseAll() }}, nil
}

// mustLocalFS is the local disk rooted at "/", as the interface uses it.
func mustLocalFS() vfs.FS {
	fsys, err := vfs.NewLocal("/")
	if err != nil {
		return nil
	}
	return fsys
}

// classify turns the two failures an agent must not retry into exit codes.
func classify(err error) error {
	switch {
	case errors.Is(err, omssh.ErrUnknownHost):
		return &exitCodeError{code: exitHostKey, err: err}
	case errors.Is(err, fs.ErrPermission):
		return &exitCodeError{code: exitPermission, err: err}
	default:
		return err
	}
}

// entryJSON is one row of a listing.
type entryJSON struct {
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
}

// guard is the safety model in one function: it runs before a filesystem is
// handed out, so every verb inherits it and there is one place to get right.
func guard(opts *Options, placeName, p string, write bool) error {
	if write && !opts.AllowWrite {
		return &exitCodeError{code: exitPermission,
			err: errors.New("refusing to change anything without --allow-write")}
	}
	if len(opts.AllowHosts) > 0 && !slices.Contains(opts.AllowHosts, placeName) {
		return &exitCodeError{code: exitPermission,
			err: fmt.Errorf("place %q is not in --allow-host", placeName)}
	}
	if len(opts.AllowPaths) > 0 {
		allowed := false
		for _, prefix := range opts.AllowPaths {
			prefix = vfs.Clean(prefix)
			if p == prefix || strings.HasPrefix(p, strings.TrimSuffix(prefix, "/")+"/") {
				allowed = true
				break
			}
		}
		if !allowed {
			return &exitCodeError{code: exitPermission,
				err: fmt.Errorf("path %q is not under any --allow-path", p)}
		}
	}
	return nil
}

// audit appends one line per change: what, where, how much, and whether it
// worked. The state directory is where history already lives.
type auditEntry struct {
	Time   string `json:"time"`
	Action string `json:"action"`
	Place  string `json:"place"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes,omitempty"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

func audit(action, placeName, p string, bytes int64, err error) {
	dir, dirErr := config.StateDir()
	if dirErr != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	entry := auditEntry{Time: time.Now().UTC().Format(time.RFC3339), Action: action,
		Place: placeName, Path: p, Bytes: bytes, OK: err == nil}
	if err != nil {
		entry.Error = err.Error()
	}
	line, encErr := json.Marshal(entry)
	if encErr != nil {
		return
	}
	file, openErr := os.OpenFile(filepath.Join(dir, "audit.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if openErr != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = file.Write(append(line, '\n'))
}

func writeJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func newFsLsCmd(opts *Options, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "ls <place>:<path>",
		Short: "List a directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, _, err := loadConfig(opts)
			if err != nil {
				return err
			}
			name, dir := splitSpec(args[0])
			target, err := openPlace(opts, cfg, name)
			if err != nil {
				return err
			}
			defer target.done()

			entries, err := target.fsys.ReadDir(dir)
			if err != nil {
				return classify(err)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			if opts.JSON {
				out := make([]entryJSON, 0, len(entries))
				for _, entry := range entries {
					info, _ := entry.Info()
					row := entryJSON{Name: entry.Name(), Dir: entry.IsDir()}
					if info != nil {
						row.Size = info.Size()
						if !info.ModTime().IsZero() {
							row.Modified = info.ModTime().UTC().Format(time.RFC3339)
						}
					}
					out = append(out, row)
				}
				return writeJSON(stdout, map[string]any{
					"place": name, "path": dir, "entries": out,
				})
			}
			for _, entry := range entries {
				line := entry.Name()
				if entry.IsDir() {
					line += "/"
				}
				fmt.Fprintln(stdout, line)
			}
			return nil
		},
	}
}

func newFsStatCmd(opts *Options, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "stat <place>:<path>",
		Short: "Describe one path",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, _, err := loadConfig(opts)
			if err != nil {
				return err
			}
			name, p := splitSpec(args[0])
			target, err := openPlace(opts, cfg, name)
			if err != nil {
				return err
			}
			defer target.done()
			info, err := target.fsys.Stat(p)
			if err != nil {
				return classify(err)
			}
			if opts.JSON {
				return writeJSON(stdout, map[string]any{
					"place": name, "path": p, "name": info.Name(), "dir": info.IsDir(),
					"size": info.Size(), "modified": info.ModTime().UTC().Format(time.RFC3339),
				})
			}
			kind := "file"
			if info.IsDir() {
				kind = "dir"
			}
			fmt.Fprintf(stdout, "%s %d %s\n", kind, info.Size(), info.Name())
			return nil
		},
	}
}

func newFsGetCmd(opts *Options, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "get <place>:<path> [local-file|-]",
		Short: "Copy one file to the local disk (or stdout)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, _, err := loadConfig(opts)
			if err != nil {
				return err
			}
			name, p := splitSpec(args[0])
			dest := "-"
			if len(args) > 1 {
				dest = args[1]
			}
			target, err := openPlace(opts, cfg, name)
			if err != nil {
				return err
			}
			defer target.done()

			file, err := target.fsys.Open(p)
			if err != nil {
				return classify(err)
			}
			defer func() { _ = file.Close() }()

			out := stdout
			if dest != "-" {
				local, err := os.Create(dest)
				if err != nil {
					return err
				}
				defer func() { _ = local.Close() }()
				out = local
			}
			written, err := io.Copy(out, file)
			if err != nil {
				return err
			}
			if opts.JSON {
				return writeJSON(stdout, map[string]any{"place": name, "path": p, "bytes": written})
			}
			if dest != "-" {
				fmt.Fprintln(stdout, dest)
			}
			return nil
		},
	}
}

func newFsPutCmd(opts *Options, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "put <local-file|-> <place>:<path>",
		Short: "Copy one file from the local disk (or stdin) to a place",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, _, err := loadConfig(opts)
			if err != nil {
				return err
			}
			name, p := splitSpec(args[1])
			if err := guard(opts, name, p, true); err != nil {
				return err
			}
			target, err := openPlace(opts, cfg, name)
			if err != nil {
				return err
			}
			defer target.done()

			var in io.Reader = os.Stdin
			if args[0] != "-" {
				file, err := os.Open(args[0])
				if err != nil {
					return err
				}
				defer func() { _ = file.Close() }()
				in = file
			}
			// A put into a directory that does not exist yet is a typo or a first
			// upload; create the parents, as the TUI's copy does.
			if parent := target.fsys.Dir(p); parent != "/" && parent != "." {
				if err := target.fsys.MkdirAll(parent, 0o755); err != nil {
					return classify(err)
				}
			}
			writer, err := target.fsys.Create(p)
			if err != nil {
				return classify(err)
			}
			written, err := io.Copy(writer, in)
			if err != nil {
				_ = writer.Close()
				audit("put", name, p, 0, err)
				return err
			}
			if err := writer.Close(); err != nil {
				audit("put", name, p, written, err)
				return err
			}
			audit("put", name, p, written, nil)
			if opts.JSON {
				return writeJSON(stdout, map[string]any{"place": name, "path": p, "bytes": written})
			}
			fmt.Fprintf(stdout, "%d bytes to %s:%s\n", written, name, p)
			return nil
		},
	}
}

func newFsRmCmd(opts *Options, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <place>:<path>",
		Short: "Remove a file or a tree",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, _, err := loadConfig(opts)
			if err != nil {
				return err
			}
			name, p := splitSpec(args[0])
			if err := guard(opts, name, p, true); err != nil {
				return err
			}
			target, err := openPlace(opts, cfg, name)
			if err != nil {
				return err
			}
			defer target.done()
			// Removing a tree is the destructive case, so it says the path it is
			// about to remove before doing it.
			if opts.JSON {
				defer func() { _ = writeJSON(stdout, map[string]any{"place": name, "path": p, "removed": true}) }()
			} else {
				fmt.Fprintf(stdout, "removing %s:%s\n", name, p)
			}
			if err := target.fsys.RemoveAll(p); err != nil {
				audit("rm", name, p, 0, err)
				return classify(err)
			}
			audit("rm", name, p, 0, nil)
			return nil
		},
	}
}

func newFsMkdirCmd(opts *Options, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "mkdir <place>:<path>",
		Short: "Create a directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, _, err := loadConfig(opts)
			if err != nil {
				return err
			}
			name, p := splitSpec(args[0])
			if err := guard(opts, name, p, true); err != nil {
				return err
			}
			target, err := openPlace(opts, cfg, name)
			if err != nil {
				return err
			}
			defer target.done()
			if err := target.fsys.MkdirAll(p, 0o755); err != nil {
				audit("mkdir", name, p, 0, err)
				return classify(err)
			}
			audit("mkdir", name, p, 0, nil)
			if opts.JSON {
				return writeJSON(stdout, map[string]any{"place": name, "path": p, "created": true})
			}
			fmt.Fprintf(stdout, "created %s:%s\n", name, p)
			return nil
		},
	}
}
