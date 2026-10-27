// Package gcs is the native vfs implementation for Google Cloud Storage: the
// JSON API rather than the S3-compatible XML one, which is still available as
// provider "gcs" with auth "s3" for HMAC keys.
//
// GCS has no directories: a "folder" is a prefix and, when it must exist while
// empty, a zero-byte marker object whose name ends in "/". Unlike Azure, its
// server-side copy needs no separate authorization, so a file rename is a real
// server-side copy and then a delete — still two calls, still not atomic, but
// the bytes stay in the bucket.
package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// Options configures one bucket.
type Options struct {
	// Bucket becomes "/".
	Bucket string
	// Prefix roots the filesystem inside the bucket: "site/" or "site".
	Prefix string
	// Auth is adc, service_account or impersonate.
	Auth string
	// CredentialsFile is a service-account JSON path (auth service_account).
	CredentialsFile string
	// CredentialsJSON is the same document, when it is kept in the keyring
	// instead of on disk.
	CredentialsJSON string
	// ServiceAccount is the account to become when Auth is impersonate.
	ServiceAccount string
	// Label is the name shown in the interface: the profile name.
	Label string
	// API replaces the SDK client in tests.
	API api
}

// FS is one bucket (or one prefix inside it) as a filesystem.
type FS struct {
	api    api
	bucket string
	prefix string
	label  string
}

// New validates the options and returns the filesystem.
func New(opts Options) (*FS, error) {
	opts.Bucket = strings.TrimSpace(opts.Bucket)
	opts.Auth = strings.ToLower(strings.TrimSpace(opts.Auth))
	if opts.Bucket == "" {
		return nil, errors.New("gcs: a bucket name is required")
	}
	if opts.Auth == "" {
		opts.Auth = AuthADC
	}
	switch opts.Auth {
	case AuthADC:
	case AuthServiceAccount:
		if strings.TrimSpace(opts.CredentialsFile) == "" && strings.TrimSpace(opts.CredentialsJSON) == "" {
			return nil, errors.New("gcs: service_account needs a credentials file or stored JSON")
		}
	case AuthImpersonate:
		if strings.TrimSpace(opts.ServiceAccount) == "" {
			return nil, errors.New("gcs: impersonate needs the service account email to become")
		}
	default:
		return nil, errors.New("gcs: auth must be adc, service_account or impersonate")
	}
	client := opts.API
	if client == nil {
		var err error
		client, err = newAPI(context.Background(), opts)
		if err != nil {
			return nil, err
		}
	}
	label := opts.Label
	if label == "" {
		label = opts.Bucket
	}
	return &FS{api: client, bucket: opts.Bucket, prefix: normalizePrefix(opts.Prefix), label: label}, nil
}

// normalizePrefix makes a prefix either empty or ending in one slash.
func normalizePrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return ""
	}
	return prefix + "/"
}

// key is the object name for a path, prefix included.
func (f *FS) key(name string) string { return f.prefix + strings.TrimPrefix(vfs.Clean(name), "/") }

// String names the filesystem for logs and error messages.
func (f *FS) String() string { return "gs://" + f.bucket + "/" + strings.TrimSuffix(f.prefix, "/") }

// Join builds a path for this filesystem.
func (f *FS) Join(elem ...string) string { return vfs.Clean(path.Join(elem...)) }

// Dir is the filesystem's own path arithmetic.
func (f *FS) Dir(name string) string { return path.Dir(name) }

// Base is the filesystem's own path arithmetic.
func (f *FS) Base(name string) string { return path.Base(name) }

// Abs resolves a path against the root.
func (f *FS) Abs(name string) (string, error) { return vfs.Clean(name), nil }

// Capabilities reports what GCS can do: no modes, no times, no links, and a real
// server-side copy.
func (f *FS) Capabilities() vfs.Capabilities {
	return vfs.Capabilities{CaseSensitive: true, ServerSideCopy: true}
}

// Stat asks what a path is.
func (f *FS) Stat(name string) (fs.FileInfo, error) { return f.stat(name) }

// Lstat is the same question here: there are no symlinks to follow.
func (f *FS) Lstat(name string) (fs.FileInfo, error) { return f.stat(name) }

func (f *FS) stat(name string) (fs.FileInfo, error) {
	clean := vfs.Clean(name)
	if clean == "/" {
		return fileInfo{name: f.label, dir: true}, nil
	}
	ctx := context.Background()
	meta, err := f.api.Stat(ctx, f.key(clean))
	if err == nil {
		return fileInfo{name: path.Base(clean), size: meta.size, modified: meta.modified, dir: meta.dir}, nil
	}
	if !errors.Is(classify(err), vfs.ErrNotExist) {
		return nil, f.wrap(name, err)
	}
	children, listErr := f.api.List(ctx, f.key(clean)+"/", "/")
	if listErr == nil && len(children) > 0 {
		return fileInfo{name: path.Base(clean), dir: true}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: vfs.ErrNotExist}
}

// ReadDir lists one level. Prefixes are directories, which is the only notion of
// a folder a bucket has.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	clean := vfs.Clean(name)
	prefix := f.key(clean)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	children, err := f.api.List(context.Background(), prefix, "/")
	if err != nil {
		return nil, f.wrap(name, err)
	}
	entries := make([]fs.DirEntry, 0, len(children))
	seen := make(map[string]bool, len(children))
	for _, meta := range children {
		base := path.Base(strings.TrimSuffix(meta.key, "/"))
		if base == "" || base == "." || meta.key == prefix || seen[base] {
			continue // the directory marker itself, or a prefix listed twice
		}
		seen[base] = true
		entries = append(entries, dirEntry{info: fileInfo{
			name: base, size: meta.size, modified: meta.modified, dir: meta.dir,
		}})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if len(entries) == 0 && clean != "/" {
		if _, err := f.Stat(clean); err != nil {
			return nil, &fs.PathError{Op: "readdir", Path: name, Err: vfs.ErrNotExist}
		}
	}
	return entries, nil
}

// Open starts a download.
func (f *FS) Open(name string) (fs.File, error) {
	clean := vfs.Clean(name)
	if clean == "/" {
		return nil, &fs.PathError{Op: "open", Path: name, Err: vfs.ErrNotExist}
	}
	body, meta, err := f.api.Open(context.Background(), f.key(clean))
	if err != nil {
		return nil, f.wrap(name, err)
	}
	return &bucketFile{
		name: path.Base(clean),
		body: body,
		info: fileInfo{name: path.Base(clean), size: meta.size, modified: meta.modified},
	}, nil
}

// ReadFile reads a whole object.
func (f *FS) ReadFile(name string) ([]byte, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

// Create makes or truncates an object.
func (f *FS) Create(name string) (vfs.WFile, error) {
	return f.OpenFile(name, vfs.O_WRONLY|vfs.O_CREATE|vfs.O_TRUNC, 0o644)
}

// OpenFile only supports writing: a bucket has no append and no random write, so
// a flag combination that asks for either is refused rather than silently doing
// something else.
func (f *FS) OpenFile(name string, flag int, _ fs.FileMode) (vfs.WFile, error) {
	write := flag&(os.O_WRONLY|os.O_RDWR) != 0
	if !write {
		file, err := f.Open(name)
		if err != nil {
			return nil, err
		}
		read, ok := file.(vfs.WFile)
		if !ok {
			_ = file.Close()
			return nil, vfs.ErrUnsupported
		}
		return read, nil
	}
	if flag&os.O_APPEND != 0 {
		return nil, fmt.Errorf("gcs: %w (no append)", vfs.ErrUnsupported)
	}
	if flag&os.O_EXCL != 0 {
		if _, err := f.Stat(name); err == nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: vfs.ErrExist}
		}
	}
	// A parent that does not exist is an error, as it is on a real filesystem:
	// a bucket would happily invent the prefix, and a typo would become a folder
	// nobody meant to create.
	if parent := vfs.Clean(f.Dir(name)); parent != "/" && parent != "." {
		if _, err := f.Stat(parent); err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: vfs.ErrNotExist}
		}
	}
	tmp, err := os.CreateTemp("", "omssh-gcs-*")
	if err != nil {
		return nil, err
	}
	return &upload{ctx: context.Background(), api: f.api, key: f.key(name), tmp: tmp}, nil
}

// Mkdir writes a directory marker, which is how an empty "folder" exists in a
// bucket at all.
func (f *FS) Mkdir(name string, _ fs.FileMode) error {
	clean := vfs.Clean(name)
	if clean == "/" {
		return &fs.PathError{Op: "mkdir", Path: name, Err: vfs.ErrExist}
	}
	if _, err := f.Stat(clean); err == nil {
		return &fs.PathError{Op: "mkdir", Path: name, Err: vfs.ErrExist}
	}
	marker := strings.TrimSuffix(f.key(clean), "/") + "/"
	if err := f.api.Put(context.Background(), marker, strings.NewReader("")); err != nil {
		return f.wrap(name, err)
	}
	return nil
}

// MkdirAll creates the final marker. The prefixes above it exist as soon as
// anything is written under them.
func (f *FS) MkdirAll(name string, perm fs.FileMode) error {
	if _, err := f.Stat(name); err == nil {
		return nil
	}
	return f.Mkdir(name, perm)
}

// Remove deletes one object. A directory with children is refused, as a real
// filesystem refuses it.
func (f *FS) Remove(name string) error {
	clean := vfs.Clean(name)
	if clean == "/" {
		return &fs.PathError{Op: "remove", Path: name, Err: vfs.ErrUnsupported}
	}
	info, err := f.Stat(clean)
	if err != nil {
		return &fs.PathError{Op: "remove", Path: name, Err: vfs.ErrNotExist}
	}
	prefix := f.key(clean)
	if info.IsDir() {
		marker := prefix + "/"
		children, listErr := f.api.List(context.Background(), marker, "/")
		if listErr != nil {
			return f.wrap(name, listErr)
		}
		for _, child := range children {
			if child.key != marker {
				return &fs.PathError{Op: "remove", Path: name, Err: errors.New("directory not empty")}
			}
		}
		prefix = marker
	}
	if err := f.api.Delete(context.Background(), prefix); err != nil {
		return f.wrap(name, err)
	}
	return nil
}

// RemoveAll deletes everything under a prefix.
func (f *FS) RemoveAll(name string) error {
	clean := vfs.Clean(name)
	if clean == "/" {
		return &fs.PathError{Op: "removeall", Path: name, Err: vfs.ErrUnsupported}
	}
	prefix := f.key(clean)
	children, err := f.api.List(context.Background(), prefix, "")
	if err != nil {
		return f.wrap(name, err)
	}
	// A flat listing matches by prefix, not by path boundary: "site" would also
	// return "site2/index.html". Only this object and the names under it belong
	// to this removal.
	for _, meta := range children {
		if meta.key != prefix && !strings.HasPrefix(meta.key, prefix+"/") {
			continue
		}
		if err := f.api.Delete(context.Background(), meta.key); err != nil && !errors.Is(classify(err), vfs.ErrNotExist) {
			return f.wrap(name, err)
		}
	}
	leftover := prefix
	if info, statErr := f.Stat(clean); statErr == nil && info.IsDir() {
		leftover = prefix + "/"
	}
	if err := f.api.Delete(context.Background(), leftover); err != nil && !errors.Is(classify(err), vfs.ErrNotExist) {
		return f.wrap(name, err)
	}
	return nil
}

// Rename is a server-side copy and then a delete. It is two calls, so it is not
// atomic, but the bytes never travel through this client.
func (f *FS) Rename(oldpath, newpath string) error {
	info, err := f.Stat(oldpath)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if !info.IsDir() {
		if err := f.api.Copy(ctx, f.key(vfs.Clean(oldpath)), f.key(vfs.Clean(newpath))); err != nil {
			return f.wrap(oldpath, err)
		}
		return f.Remove(oldpath)
	}

	sourcePrefix := f.key(vfs.Clean(oldpath)) + "/"
	targetPrefix := f.key(vfs.Clean(newpath)) + "/"
	children, err := f.api.List(ctx, sourcePrefix, "")
	if err != nil {
		return f.wrap(oldpath, err)
	}
	for _, meta := range children {
		if meta.key != sourcePrefix && !strings.HasPrefix(meta.key, sourcePrefix) {
			continue
		}
		target := targetPrefix + strings.TrimPrefix(meta.key, sourcePrefix)
		if err := f.api.Copy(ctx, meta.key, target); err != nil {
			return f.wrap(oldpath, err)
		}
	}
	return f.RemoveAll(oldpath)
}

// Chmod is not supported: objects have no permission bits.
func (f *FS) Chmod(string, fs.FileMode) error { return vfs.ErrUnsupported }

// Chtimes is not supported: the modification time is set by the upload.
func (f *FS) Chtimes(string, time.Time, time.Time) error { return vfs.ErrUnsupported }

// Readlink is not supported: objects are not links.
func (f *FS) Readlink(string) (string, error) { return "", vfs.ErrUnsupported }

// Symlink is not supported: objects are not links.
func (f *FS) Symlink(string, string) error { return vfs.ErrUnsupported }

// Truncate rewrites the object: read it, cut or pad it, put it back. It is the
// one operation here that costs the whole file.
func (f *FS) Truncate(name string, size int64) error {
	if size < 0 {
		return &fs.PathError{Op: "truncate", Path: name, Err: fs.ErrInvalid}
	}
	data, err := f.ReadFile(name)
	if err != nil {
		return err
	}
	if int64(len(data)) > size {
		data = data[:size]
	} else {
		data = append(data, make([]byte, size-int64(len(data)))...)
	}
	if err := f.api.Put(context.Background(), f.key(name), strings.NewReader(string(data))); err != nil {
		return f.wrap(name, err)
	}
	return nil
}

// wrap turns a classified SDK error into a path error with the sentinel callers
// branch on.
func (f *FS) wrap(name string, err error) error {
	switch classified := classify(err); {
	case errors.Is(classified, vfs.ErrNotExist):
		return &fs.PathError{Op: "open", Path: name, Err: vfs.ErrNotExist}
	case errors.Is(classified, vfs.ErrPermission):
		return &fs.PathError{Op: "open", Path: name, Err: vfs.ErrPermission}
	default:
		return fmt.Errorf("%s: %w", f, err)
	}
}
