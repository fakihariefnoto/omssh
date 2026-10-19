package vfs

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Local is an FS over the local disk, rooted at a directory. A root of "/" gives
// the whole machine; the file manager uses that, tests use a temporary
// directory.
//
// Every path is cleaned and confined to the root, so a name arriving from the
// network or from a crafted archive cannot climb out.
type Local struct {
	root string
}

// NewLocal returns a filesystem rooted at dir. The directory must exist.
func NewLocal(dir string) (*Local, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "root", Path: abs, Err: fs.ErrInvalid}
	}
	return &Local{root: filepath.Clean(abs)}, nil
}

// Root returns the directory this filesystem is rooted at.
func (l *Local) Root() string { return l.root }

// String names the filesystem for logs: the root, abbreviated for a home
// directory.
func (l *Local) String() string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(l.root, home) {
		return "~" + strings.TrimPrefix(l.root, home)
	}
	return l.root
}

// Capabilities reports that a local disk can do everything, and that a copy is a
// kernel call rather than a round trip through us.
func (l *Local) Capabilities() Capabilities {
	return Capabilities{
		Symlink:        true,
		Chmod:          true,
		Chtimes:        true,
		CopyData:       false, // SFTP-only extension
		ServerSideCopy: true,  // cp -a, and rename within the same filesystem
		CaseSensitive:  !isCaseInsensitive(),
	}
}

// Join builds a path inside the root.
func (l *Local) Join(elem ...string) string {
	cleaned := make([]string, 0, len(elem))
	for _, e := range elem {
		cleaned = append(cleaned, strings.Trim(e, "/"))
	}
	return filepath.Join(cleaned...)
}

// Dir is the platform's path arithmetic, so the local side behaves exactly like
// the shell beside it. The result is slash-separated, like every vfs path.
func (l *Local) Dir(name string) string { return filepath.ToSlash(filepath.Dir(name)) }

// Base returns the last element of a path.
func (l *Local) Base(name string) string { return filepath.Base(name) }

// real maps a filesystem-relative name onto the disk, refusing to escape the
// root.
func (l *Local) real(name string) (string, error) {
	cleaned := Clean(name)
	if cleaned == "/" {
		return l.root, nil
	}
	full := filepath.Join(l.root, filepath.FromSlash(strings.TrimPrefix(cleaned, "/")))
	// Belt and braces: Clean already removed "..", but a symlink inside the root
	// could still point outside it, and only the caller can decide whether that
	// is acceptable.
	rel, err := filepath.Rel(l.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &fs.PathError{Op: "resolve", Path: name, Err: fs.ErrPermission}
	}
	return full, nil
}

// Abs resolves a name to an absolute path on this machine.
func (l *Local) Abs(name string) (string, error) { return l.real(name) }

// Open opens a file for reading.
func (l *Local) Open(name string) (fs.File, error) {
	full, err := l.real(name)
	if err != nil {
		return nil, err
	}
	return os.Open(full)
}

// OpenFile opens with explicit flags, wrapping the handle so it satisfies WFile.
func (l *Local) OpenFile(name string, flag int, perm fs.FileMode) (WFile, error) {
	full, err := l.real(name)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(full, flag, perm)
	if err != nil {
		return nil, err
	}
	return &localFile{File: file}, nil
}

// Create makes or truncates a file.
func (l *Local) Create(name string) (WFile, error) {
	return l.OpenFile(name, O_RDWR|O_CREATE|O_TRUNC, 0o644)
}

// ReadDir lists a directory.
func (l *Local) ReadDir(name string) ([]fs.DirEntry, error) {
	full, err := l.real(name)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(full)
}

// Stat and Lstat: Lstat does not follow a final symlink.
func (l *Local) Stat(name string) (fs.FileInfo, error) {
	full, err := l.real(name)
	if err != nil {
		return nil, err
	}
	return os.Stat(full)
}

// Lstat stats the link itself.
func (l *Local) Lstat(name string) (fs.FileInfo, error) {
	full, err := l.real(name)
	if err != nil {
		return nil, err
	}
	return os.Lstat(full)
}

// ReadFile reads a whole file.
func (l *Local) ReadFile(name string) ([]byte, error) {
	full, err := l.real(name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}

// Mkdir creates one directory.
func (l *Local) Mkdir(name string, perm fs.FileMode) error {
	full, err := l.real(name)
	if err != nil {
		return err
	}
	return os.Mkdir(full, perm)
}

// MkdirAll creates a directory and its parents.
func (l *Local) MkdirAll(name string, perm fs.FileMode) error {
	full, err := l.real(name)
	if err != nil {
		return err
	}
	return os.MkdirAll(full, perm)
}

// Remove deletes one file or an empty directory.
func (l *Local) Remove(name string) error {
	full, err := l.real(name)
	if err != nil {
		return err
	}
	if IsRoot(name) {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrPermission}
	}
	return os.Remove(full)
}

// RemoveAll deletes a tree. The root itself is never removed.
func (l *Local) RemoveAll(name string) error {
	if IsRoot(name) {
		return &fs.PathError{Op: "removeall", Path: name, Err: fs.ErrPermission}
	}
	full, err := l.real(name)
	if err != nil {
		return err
	}
	return os.RemoveAll(full)
}

// Rename moves a file or directory. Within one filesystem this is a link
// operation, which is why the transfer engine prefers it.
func (l *Local) Rename(oldpath, newpath string) error {
	from, err := l.real(oldpath)
	if err != nil {
		return err
	}
	to, err := l.real(newpath)
	if err != nil {
		return err
	}
	return os.Rename(from, to)
}

// Chmod sets permission bits.
func (l *Local) Chmod(name string, mode fs.FileMode) error {
	full, err := l.real(name)
	if err != nil {
		return err
	}
	return os.Chmod(full, mode)
}

// Chtimes sets access and modification times.
func (l *Local) Chtimes(name string, atime, mtime time.Time) error {
	full, err := l.real(name)
	if err != nil {
		return err
	}
	return os.Chtimes(full, atime, mtime)
}

// Readlink returns a symlink's target.
func (l *Local) Readlink(name string) (string, error) {
	full, err := l.real(name)
	if err != nil {
		return "", err
	}
	return os.Readlink(full)
}

// Symlink creates a symbolic link.
func (l *Local) Symlink(target, name string) error {
	full, err := l.real(name)
	if err != nil {
		return err
	}
	return os.Symlink(target, full)
}

// Truncate resizes a file.
func (l *Local) Truncate(name string, size int64) error {
	full, err := l.real(name)
	if err != nil {
		return err
	}
	return os.Truncate(full, size)
}

// localFile adapts *os.File to WFile.
type localFile struct{ *os.File }

// Sync flushes to stable storage, which matters for a rename-then-replace
// pattern: without it a crash can leave the temporary file and no rename.
func (f *localFile) Sync() error { return f.File.Sync() }

// ReaderFrom keeps io.Copy's fast paths available.
func (f *localFile) ReaderFrom(r io.Reader) (int64, error) { return io.Copy(f.File, r) }

// isCaseInsensitive reports whether the platform's filesystem ignores case.
// There is no portable way to ask, so this is the same heuristic everyone uses:
// the separator that Windows uses is the one that gives it away.
func isCaseInsensitive() bool { return os.PathSeparator == '\\' }
