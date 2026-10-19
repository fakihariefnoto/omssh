// Package sftpfs is the vfs.FS implementation over SFTP.
//
// It is deliberately the same shape as the local one, including the parts that
// are awkward over a protocol: paths are cleaned and rooted, errors are mapped
// onto the standard sentinels, and listings are sorted. Everything above it —
// copy, extract, compress, deploy — is written against vfs.FS and cannot tell
// which side it is talking to (ADR 0001).
package sftpfs

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// ErrClosed is returned by every operation after Close. The library panics when
// its client is used after closing, which a file manager must never do, so this
// wrapper refuses first.
var ErrClosed = errors.New("sftpfs: the connection is closed")

// SFTP is a filesystem over an SFTP session.
type SFTP struct {
	mu     sync.RWMutex
	closed bool
	client *sftp.Client
	// base is the directory "/" maps to. It is the server's working directory —
	// the user's home, in practice — so a path in the interface reads like a path
	// a person would type.
	base  string
	label string
}

// New wraps an existing sftp client. base is the directory "/" maps to; an empty
// base means the server's working directory.
func New(client *sftp.Client, base, label string) (*SFTP, error) {
	if client == nil {
		return nil, errors.New("sftpfs: no client")
	}
	if base == "" {
		workdir, err := client.Getwd()
		if err != nil {
			// An old server may not implement realpath; the root is a safe,
			// documented fallback and the interface never shows it unasked.
			workdir = "/"
		}
		base = workdir
	}
	return &SFTP{client: client, base: path.Clean(base), label: label}, nil
}

// Close ends the SFTP session. The SSH connection itself belongs to the pool.
func (s *SFTP) Close() error {
	s.mu.Lock()
	if s.closed || s.client == nil {
		s.closed = true
		s.mu.Unlock()
		return nil
	}
	client := s.client
	s.client = nil
	s.closed = true
	s.mu.Unlock()
	return client.Close()
}

// checkOpen reports whether the session is still usable.
func (s *SFTP) checkOpen() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.client == nil {
		return ErrClosed
	}
	return nil
}

// String names the filesystem for logs.
func (s *SFTP) String() string {
	if s.label == "" {
		return "sftp:" + s.base
	}
	return s.label
}

// Root is the server-side directory that "/" maps to, for display.
func (s *SFTP) Root() string { return s.base }

// Capabilities reports what SFTP can do: modes, times and symlinks, but a copy
// still travels through us unless the server advertises copy-data.
func (s *SFTP) Capabilities() vfs.Capabilities {
	return vfs.Capabilities{
		Symlink:        true,
		Chmod:          true,
		Chtimes:        true,
		CopyData:       false,
		ServerSideCopy: false,
		CaseSensitive:  true,
	}
}

// resolve turns an interface path into one the server understands, confined to
// the base the way the local filesystem is confined to its root.
func (s *SFTP) resolve(name string) string {
	cleaned := strings.TrimPrefix(vfs.Clean(name), "/")
	if cleaned == "" {
		return s.base
	}
	return path.Join(s.base, cleaned)
}

// Open opens a file for reading.
func (s *SFTP) Open(name string) (fs.File, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	file, err := s.client.Open(s.resolve(name))
	if err != nil {
		return nil, s.wrap("open", name, err)
	}
	return file, nil
}

// OpenFile opens with explicit flags.
func (s *SFTP) OpenFile(name string, flag int, perm fs.FileMode) (vfs.WFile, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	file, err := s.client.OpenFile(s.resolve(name), flag)
	if err != nil {
		return nil, s.wrap("open", name, err)
	}
	if flag&vfs.O_CREATE != 0 && perm != 0 {
		// SFTP's open carries no mode, so the bits are applied afterwards. The
		// error is not fatal: a file created with the server's umask is better
		// than no file, and the caller reports it if it matters.
		_ = s.client.Chmod(s.resolve(name), perm)
	}
	return &remoteFile{File: file}, nil
}

// Create makes or truncates a file.
func (s *SFTP) Create(name string) (vfs.WFile, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	file, err := s.client.Create(s.resolve(name))
	if err != nil {
		return nil, s.wrap("create", name, err)
	}
	return &remoteFile{File: file}, nil
}

// ReadDir lists a directory, sorted by name the way every other implementation
// of this interface does.
func (s *SFTP) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	entries, err := s.client.ReadDir(s.resolve(name))
	if err != nil {
		return nil, s.wrap("readdir", name, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	out := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, dirEntry{entry})
	}
	return out, nil
}

// Stat follows a final symlink.
func (s *SFTP) Stat(name string) (fs.FileInfo, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	info, err := s.client.Stat(s.resolve(name))
	if err != nil {
		return nil, s.wrap("stat", name, err)
	}
	return info, nil
}

// Lstat does not follow a final symlink.
func (s *SFTP) Lstat(name string) (fs.FileInfo, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	info, err := s.client.Lstat(s.resolve(name))
	if err != nil {
		return nil, s.wrap("lstat", name, err)
	}
	return info, nil
}

// ReadFile reads a whole file. SFTP has no readfile request, so it is an open
// and a read, which is what every client does.
func (s *SFTP) ReadFile(name string) ([]byte, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	file, err := s.client.Open(s.resolve(name))
	if err != nil {
		return nil, s.wrap("read", name, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, s.wrap("read", name, err)
	}
	return data, nil
}

// Mkdir creates one directory.
func (s *SFTP) Mkdir(name string, perm fs.FileMode) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	target := s.resolve(name)
	if err := s.client.Mkdir(target); err != nil {
		return s.wrap("mkdir", name, err)
	}
	if perm != 0 {
		_ = s.client.Chmod(target, perm)
	}
	return nil
}

// MkdirAll creates a directory and its parents.
func (s *SFTP) MkdirAll(name string, perm fs.FileMode) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	target := s.resolve(name)
	if err := s.client.MkdirAll(target); err != nil {
		return s.wrap("mkdirall", name, err)
	}
	if perm != 0 {
		_ = s.client.Chmod(target, perm)
	}
	return nil
}

// Remove deletes one file or an empty directory. The root is never removed.
func (s *SFTP) Remove(name string) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if vfs.IsRoot(name) {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrPermission}
	}
	if err := s.client.Remove(s.resolve(name)); err != nil {
		return s.wrap("remove", name, err)
	}
	return nil
}

// RemoveAll deletes a tree. The root is never removed.
//
// A path that is already gone is not an error, matching os.RemoveAll and the
// local implementation: this is what makes it safe to call after a partial
// failure without checking first. The protocol reports "no such file" rather
// than success, so that one status is swallowed.
func (s *SFTP) RemoveAll(name string) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if vfs.IsRoot(name) {
		return &fs.PathError{Op: "removeall", Path: name, Err: fs.ErrPermission}
	}
	if err := s.client.RemoveAll(s.resolve(name)); err != nil {
		// Only on the way out: a missing *parent* is also reported this way, and
		// the caller asked for a tree to be gone, which it is.
		wrapped := s.wrap("removeall", name, err)
		if errors.Is(wrapped, fs.ErrNotExist) {
			return nil
		}
		return wrapped
	}
	return nil
}

// Rename moves a file or directory.
//
// The v3 rename request fails when the destination exists, which is not what a
// file manager means by rename. The OpenSSH posix-rename extension is tried
// first, and the standard request is the fallback for servers without it.
func (s *SFTP) Rename(oldpath, newpath string) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	from, to := s.resolve(oldpath), s.resolve(newpath)
	if err := s.client.PosixRename(from, to); err != nil {
		if status, ok := asStatus(err); ok && status.Code == codeOpUnsupported {
			if err := s.client.Rename(from, to); err != nil {
				return s.wrap("rename", oldpath, err)
			}
			return nil
		}
		return s.wrap("rename", oldpath, err)
	}
	return nil
}

// Chmod sets permission bits.
func (s *SFTP) Chmod(name string, mode fs.FileMode) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := s.client.Chmod(s.resolve(name), mode); err != nil {
		return s.wrap("chmod", name, err)
	}
	return nil
}

// Chtimes sets access and modification times.
func (s *SFTP) Chtimes(name string, atime, mtime time.Time) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := s.client.Chtimes(s.resolve(name), atime, mtime); err != nil {
		return s.wrap("chtimes", name, err)
	}
	return nil
}

// Readlink returns a symlink's target.
func (s *SFTP) Readlink(name string) (string, error) {
	if err := s.checkOpen(); err != nil {
		return "", err
	}
	target, err := s.client.ReadLink(s.resolve(name))
	if err != nil {
		return "", s.wrap("readlink", name, err)
	}
	return target, nil
}

// Symlink creates a symbolic link.
func (s *SFTP) Symlink(target, name string) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := s.client.Symlink(target, s.resolve(name)); err != nil {
		return s.wrap("symlink", name, err)
	}
	return nil
}

// Truncate resizes a file.
func (s *SFTP) Truncate(name string, size int64) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := s.client.Truncate(s.resolve(name), size); err != nil {
		return s.wrap("truncate", name, err)
	}
	return nil
}

// Join builds a path the way the interface promises: slash-separated and rooted.
func (s *SFTP) Join(elem ...string) string {
	parts := make([]string, 0, len(elem))
	for _, e := range elem {
		parts = append(parts, strings.Trim(e, "/"))
	}
	return vfs.Clean(path.Join(parts...))
}

// Dir is pure path arithmetic, like filepath.Dir: given a path it returns its
// parent in the same shape, without adding or removing a root. Paths are
// slash-separated on every server, whatever the server itself runs on.
func (s *SFTP) Dir(name string) string { return path.Dir(name) }

// Base returns the last element of a path, as filepath.Base would.
func (s *SFTP) Base(name string) string { return path.Base(name) }

// Abs resolves a name to the absolute path it means on the server.
func (s *SFTP) Abs(name string) (string, error) { return s.resolve(name), nil }

// remoteFile adapts sftp.File to vfs.WFile.
type remoteFile struct{ *sftp.File }

// Sync has nothing to do here: SFTP has no fsync request, and pretending
// otherwise would be worse than saying so. A caller that needs durability
// renames into place, which is the pattern the transfer engine uses anyway.
func (f *remoteFile) Sync() error { return nil }

// dirEntry adapts os.FileInfo to fs.DirEntry.
type dirEntry struct{ fs.FileInfo }

// Type is the file mode's type bits.
func (e dirEntry) Type() fs.FileMode { return e.Mode().Type() }

// Info is the entry's FileInfo, already at hand.
func (e dirEntry) Info() (fs.FileInfo, error) { return e.FileInfo, nil }

// wrap maps an SFTP failure onto the sentinel the interface promises, so callers
// can branch on fs.ErrNotExist and friends whatever the server said.
func (s *SFTP) wrap(op, name string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) {
		// EOF is how SFTP reports a directory listing that has been fully read
		// and a file at its end; it is not a failure of the operation.
		return err
	}
	status, ok := asStatus(err)
	if !ok {
		return &fs.PathError{Op: op, Path: name, Err: err}
	}
	switch status.Code {
	case codeNoSuchFile:
		return &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
	case codePermissionDenied:
		return &fs.PathError{Op: op, Path: name, Err: fs.ErrPermission}
	case codeFileAlreadyExists:
		return &fs.PathError{Op: op, Path: name, Err: fs.ErrExist}
	case codeOpUnsupported:
		return &fs.PathError{Op: op, Path: name, Err: vfs.ErrUnsupported}
	default:
		return &fs.PathError{Op: op, Path: name, Err: err}
	}
}

// SSH FX status codes this package maps. They are defined here rather than taken
// from the library because the library does not export them all, and a wrong
// number would be a silent misclassification.
const (
	codeNoSuchFile        = 2
	codePermissionDenied  = 3
	codeFileAlreadyExists = 11
	codeOpUnsupported     = 8
)

// asStatus extracts the server's status code from an error.
func asStatus(err error) (*sftp.StatusError, bool) {
	var status *sftp.StatusError
	if errors.As(err, &status) {
		return status, true
	}
	return nil, false
}
