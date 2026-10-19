// Package vfs is omssh's one filesystem interface, implemented over the local
// disk and over SFTP (ADR 0001).
//
// Everything the product promises — copy in four directions, extract an archive
// from one side into the other, deploy a tree, diff two trees — is the same
// question asked in different clothes: given a source that yields bytes and a
// destination that accepts bytes, move them and keep the shape. So there is
// exactly one abstraction, and there is deliberately no Upload and no Download.
package vfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"time"
)

// Capabilities describes what a filesystem can actually do, so callers degrade
// instead of discovering limits through errors. A server without symlink support
// should be reported up front, not when a deploy is half finished.
type Capabilities struct {
	// Symlink is false when the server cannot create symbolic links.
	Symlink bool
	// Chmod is false for filesystems without Unix modes.
	Chmod bool
	// Chtimes is false when modification times cannot be set.
	Chtimes bool
	// CopyData is true when the server supports the SFTP copy-data extension,
	// which copies a file server-side without moving bytes through us.
	CopyData bool
	// ServerSideCopy is true when a shell copy (cp -a) is possible on this host.
	ServerSideCopy bool
	// CaseSensitive describes how names are compared.
	CaseSensitive bool
}

// WFile is a file opened for writing.
type WFile interface {
	io.Writer
	io.Closer
	// ReaderFrom lets the copy engine use the fastest path the platform offers
	// (sendfile, copy_file_range, io.Copy's buffer pool).
	io.ReaderFrom
	// Sync flushes to stable storage where the platform supports it.
	Sync() error
}

// FS is the intersection of behaviour omssh needs, deliberately close to io/fs
// so the standard library's helpers (WalkDir, Glob, ReadFile) keep working.
//
// Paths are slash-separated and rooted: they always start with "/", which is the
// filesystem's own root, not the machine's. Join produces that form and every
// method accepts it; a path without the leading slash is cleaned into the same
// shape, because callers frequently build one from a listing.
type FS interface {
	fs.FS
	fs.ReadDirFS
	fs.StatFS
	fs.ReadFileFS

	// Lstat stats the path itself, not what a symlink points at.
	Lstat(name string) (fs.FileInfo, error)
	// Create makes or truncates a file for writing.
	Create(name string) (WFile, error)
	// OpenFile opens with explicit flags and permissions.
	OpenFile(name string, flag int, perm fs.FileMode) (WFile, error)
	Mkdir(name string, perm fs.FileMode) error
	MkdirAll(name string, perm fs.FileMode) error
	Remove(name string) error
	RemoveAll(name string) error
	Rename(oldpath, newpath string) error
	Chmod(name string, mode fs.FileMode) error
	Chtimes(name string, atime, mtime time.Time) error
	Readlink(name string) (string, error)
	Symlink(target, name string) error
	Truncate(name string, size int64) error

	// Join builds a path for this filesystem.
	Join(elem ...string) string
	// Dir and Base are the filesystem's own path arithmetic.
	Dir(name string) string
	Base(name string) string
	// Abs resolves a path against the root, for display and for logs.
	Abs(name string) (string, error)
	// Capabilities reports what this filesystem can do.
	Capabilities() Capabilities
	// String names the filesystem, for logs and error messages.
	String() string
}

// Errors the UI branches on, so it can say something useful instead of printing
// whatever the server said.
var (
	// ErrNotExist is returned when a path is missing.
	ErrNotExist = fs.ErrNotExist
	// ErrExist is returned when a path already exists.
	ErrExist = fs.ErrExist
	// ErrPermission is returned when the filesystem refused.
	ErrPermission = fs.ErrPermission
	// ErrUnsupported is returned for an operation the filesystem cannot do.
	ErrUnsupported = errors.New("vfs: operation not supported by this filesystem")
)

// Clean normalises a name the way the interface promises: slash-separated, with
// a leading slash and any "." or ".." resolved, but never escaping the root.
//
// It is the only place path traversal is handled, so a caller cannot forget.
func Clean(name string) string {
	if name == "" || name == "/" {
		return "/"
	}
	out := make([]string, 0, 8)
	for _, part := range split(name) {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, part)
	}
	return "/" + joinSlash(out)
}

// IsRoot reports whether a cleaned path is the filesystem's root.
func IsRoot(name string) bool { return Clean(name) == "/" }

// split breaks a path on slashes, whatever the input platform.
func split(name string) []string {
	out := make([]string, 0, 8)
	start := 0
	for i := 0; i < len(name); i++ {
		if name[i] == '/' {
			out = append(out, name[start:i])
			start = i + 1
		}
	}
	return append(out, name[start:])
}

func joinSlash(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	n := len(parts) - 1
	for _, p := range parts {
		n += len(p)
	}
	buf := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			buf = append(buf, '/')
		}
		buf = append(buf, p...)
	}
	return string(buf)
}

// ReadAll reads a whole file, so callers do not repeat the open/close dance.
func ReadAll(fsys FS, name string) ([]byte, error) {
	file, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

// WriteFile writes data to a path, creating it if needed.
func WriteFile(fsys FS, name string, data []byte, perm fs.FileMode) error {
	file, err := fsys.OpenFile(name, O_WRONLY|O_CREATE|O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// Open flags, so callers do not have to import os for the common cases.
//
// These alias the os package rather than repeating its bit patterns: the values
// differ between Linux and macOS, and a hand-written table is wrong on one of
// them. An implementation over a protocol with its own flag vocabulary (SFTP's
// FxCreate/FxTrunc/FxAppend) translates them itself, which is a few lines and
// the right place for the knowledge.
const (
	O_RDONLY = os.O_RDONLY
	O_WRONLY = os.O_WRONLY
	O_RDWR   = os.O_RDWR
	O_APPEND = os.O_APPEND
	O_CREATE = os.O_CREATE
	O_EXCL   = os.O_EXCL
	O_TRUNC  = os.O_TRUNC
)
