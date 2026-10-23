// Package webdav is the vfs implementation for WebDAV servers: Nextcloud, ownCloud,
// Box, SharePoint, and the plain Apache mod_dav on someone's NAS.
//
// WebDAV is the one remote protocol here that is actually a filesystem: real
// directories, real MOVE (an atomic rename, not copy-then-delete), ETags and
// If-Match for conflict-safe writes. What it does not have is permissions or
// symlinks, and it says so.
package webdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// Options configures one server.
type Options struct {
	// BaseURL is the collection that becomes "/": for Nextcloud that is
	// https://host/remote.php/dav/files/<user>/.
	BaseURL  string
	Username string
	Password string
	Label    string
	// Client is for tests and for a proxy; nil means a client with a timeout.
	Client *http.Client
}

// FS is one WebDAV collection as a filesystem.
type FS struct {
	base   *url.URL
	user   string
	pass   string
	label  string
	client *http.Client
}

// New validates the options and returns the filesystem.
func New(opts Options) (*FS, error) {
	raw := strings.TrimSpace(opts.BaseURL)
	if raw == "" {
		return nil, errors.New("webdav: a URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("webdav: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("webdav: the URL must start with http:// or https://")
	}
	// The base always ends in a slash, so joining a path is appending.
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	label := opts.Label
	if label == "" {
		label = parsed.Host
	}
	return &FS{base: parsed, user: opts.Username, pass: opts.Password, label: label, client: client}, nil
}

// pathOf turns a vfs path into the request URL.
func (f *FS) pathOf(name string) string {
	clean := strings.TrimPrefix(vfs.Clean(name), "/")
	if clean == "" {
		return f.base.String()
	}
	u := *f.base
	// Escape each segment but keep the slashes: a name with a space must work.
	parts := strings.Split(clean, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	u.Path = f.base.Path + strings.Join(parts, "/")
	return u.String()
}

// Join builds a path the way the interface expects.
func (f *FS) Join(elem ...string) string { return vfs.Clean(path.Join(elem...)) }

// Dir returns the parent collection.
func (f *FS) Dir(name string) string { return path.Dir(name) }

// Base returns the last element of a path.
func (f *FS) Base(name string) string { return path.Base(name) }

// Abs resolves a path against the root.
func (f *FS) Abs(name string) (string, error) { return vfs.Clean(name), nil }

// String names the filesystem for logs and error messages.
func (f *FS) String() string { return "webdav:" + f.base.Host + f.base.Path }

// Capabilities reports a real rename and no permissions or links.
func (f *FS) Capabilities() vfs.Capabilities {
	return vfs.Capabilities{ServerSideCopy: true, CaseSensitive: true}
}

// do runs a request with credentials.
func (f *FS) do(ctx context.Context, method, target string, body io.Reader, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if f.user != "" || f.pass != "" {
		req.SetBasicAuth(f.user, f.pass)
	}
	for key, value := range header {
		req.Header.Set(key, value)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		_ = resp.Body.Close()
		return nil, &fs.PathError{Op: method, Path: target, Err: vfs.ErrPermission}
	case http.StatusNotFound:
		_ = resp.Body.Close()
		return nil, &fs.PathError{Op: method, Path: target, Err: vfs.ErrNotExist}
	case http.StatusConflict:
		// A PUT into a collection that does not exist, which is a missing parent
		// as far as the interface is concerned.
		_ = resp.Body.Close()
		return nil, &fs.PathError{Op: method, Path: target, Err: vfs.ErrNotExist}
	}
	return resp, nil
}

// multistatus is the WebDAV answer to PROPFIND.
type multistatus struct {
	Responses []response `xml:"response"`
}

type response struct {
	Href     string     `xml:"href"`
	Propstat []propstat `xml:"propstat"`
}

type propstat struct {
	Prop   prop   `xml:"prop"`
	Status string `xml:"status"`
}

type prop struct {
	ResourceType struct {
		Collection *struct{} `xml:"collection"`
	} `xml:"resourcetype"`
	ContentLength string `xml:"getcontentlength"`
	LastModified  string `xml:"getlastmodified"`
	DisplayName   string `xml:"displayname"`
}

// propfind asks for one or two levels and returns the responses, without the
// collection that was asked about (Depth 1 includes it).
func (f *FS) propfind(ctx context.Context, name, depth string) ([]response, error) {
	body := `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:"><d:prop>
<d:resourcetype/><d:getcontentlength/><d:getlastmodified/>
</d:prop></d:propfind>`
	resp, err := f.do(ctx, "PROPFIND", f.pathOf(name), strings.NewReader(body), map[string]string{
		"Depth":        depth,
		"Content-Type": "application/xml; charset=utf-8",
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("webdav: propfind %s: %s", name, resp.Status)
	}
	var parsed multistatus
	if err := xml.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("webdav: reading propfind answer: %w", err)
	}
	return parsed.Responses, nil
}

// entryFrom turns one response into a DirEntry, relative to name.
func (f *FS) entryFrom(name string, r response) (fs.DirEntry, bool) {
	// href is an absolute path; make it relative to the base collection.
	href, err := url.PathUnescape(r.Href)
	if err != nil {
		return nil, false
	}
	basePath := f.base.Path
	if !strings.HasPrefix(href, basePath) {
		return nil, false
	}
	rel := strings.TrimSuffix(strings.TrimPrefix(href, basePath), "/")
	if rel == "" {
		return nil, false // the collection itself
	}
	if rel == strings.TrimPrefix(vfs.Clean(name), "/") {
		return nil, false
	}
	entryPath := path.Base(rel)
	if entryPath == "" || entryPath == "." {
		return nil, false
	}
	info := fileInfo{name: entryPath}
	for _, ps := range r.Propstat {
		if !strings.Contains(ps.Status, "200") {
			continue
		}
		if ps.Prop.ResourceType.Collection != nil {
			info.dir = true
		}
		if ps.Prop.ContentLength != "" {
			_, _ = fmt.Sscanf(ps.Prop.ContentLength, "%d", &info.size)
		}
		if ps.Prop.LastModified != "" {
			if t, err := http.ParseTime(ps.Prop.LastModified); err == nil {
				info.modified = t
			}
		}
	}
	return dirEntry{info: info}, true
}

// ReadDir lists one collection.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	responses, err := f.propfind(context.Background(), name, "1")
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, 0, len(responses))
	for _, r := range responses {
		if entry, ok := f.entryFrom(name, r); ok {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// Stat asks about one path.
func (f *FS) Stat(name string) (fs.FileInfo, error) {
	if vfs.Clean(name) == "/" {
		return fileInfo{name: f.label, dir: true}, nil
	}
	responses, err := f.propfind(context.Background(), name, "0")
	if err != nil {
		return nil, err
	}
	if len(responses) == 0 {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: vfs.ErrNotExist}
	}
	first := responses[0]
	if entry, ok := f.entryFrom(name, first); ok {
		return entry.Info()
	}
	// Depth 0 may include the collection itself, which entryFrom drops.
	info := fileInfo{name: path.Base(vfs.Clean(name))}
	for _, ps := range first.Propstat {
		if ps.Prop.ResourceType.Collection != nil {
			info.dir = true
		}
		if ps.Prop.ContentLength != "" {
			_, _ = fmt.Sscanf(ps.Prop.ContentLength, "%d", &info.size)
		}
		if ps.Prop.LastModified != "" {
			if t, err := http.ParseTime(ps.Prop.LastModified); err == nil {
				info.modified = t
			}
		}
	}
	return info, nil
}

// Lstat is Stat: WebDAV has no symlinks.
func (f *FS) Lstat(name string) (fs.FileInfo, error) { return f.Stat(name) }

// Open reads a file.
func (f *FS) Open(name string) (fs.File, error) {
	ctx := context.Background()
	resp, err := f.do(ctx, http.MethodGet, f.pathOf(name), nil, nil) //nolint:bodyclose // the body is remoteFile's
	if err != nil {
		return nil, err
	}
	// The body belongs to remoteFile now; a close on this error path would close
	// the file the caller is about to read.
	size := resp.ContentLength
	modified := time.Time{}
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		modified = t
	}
	return &remoteFile{
		name: path.Base(vfs.Clean(name)),
		body: resp.Body,
		info: fileInfo{name: path.Base(vfs.Clean(name)), size: size, modified: modified},
	}, nil
}

// ReadFile reads a whole file.
func (f *FS) ReadFile(name string) ([]byte, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

// Create and OpenFile write through a local buffer: PUT with a known length is what
// every server understands, and a partial object is worse than a slow one.
func (f *FS) Create(name string) (vfs.WFile, error) {
	return f.OpenFile(name, vfs.O_WRONLY|vfs.O_CREATE|vfs.O_TRUNC, 0o644)
}

// OpenFile supports reading and whole-file writes; WebDAV has no append and no
// random write, so those flags are refused rather than approximated.
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
		return nil, fmt.Errorf("webdav: %w (no append)", vfs.ErrUnsupported)
	}
	if flag&os.O_EXCL != 0 {
		if _, err := f.Stat(name); err == nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: vfs.ErrExist}
		}
	}
	// A parent that does not exist is an error now, not at Close: a PUT into a
	// missing collection is a 409 nobody wants to discover after typing.
	if parent := vfs.Clean(f.Dir(name)); parent != "/" && parent != "." {
		if _, err := f.Stat(parent); err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: vfs.ErrNotExist}
		}
	}
	tmp, err := os.CreateTemp("", "omssh-dav-*")
	if err != nil {
		return nil, err
	}
	return &upload{fs: f, name: vfs.Clean(name), tmp: tmp}, nil
}

// Mkdir makes one collection.
func (f *FS) Mkdir(name string, _ fs.FileMode) error {
	ctx := context.Background()
	resp, err := f.do(ctx, "MKCOL", f.pathOf(name)+"/", nil, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webdav: mkcol %s: %s", name, resp.Status)
	}
	return nil
}

// MkdirAll makes each missing level. A server that answers 405 means it already
// exists, which is not an error here.
func (f *FS) MkdirAll(name string, perm fs.FileMode) error {
	clean := vfs.Clean(name)
	if clean == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	prefix := ""
	for _, part := range parts {
		prefix = path.Join(prefix, part)
		if _, err := f.Stat("/" + prefix); err == nil {
			continue
		}
		if err := f.Mkdir("/"+prefix, perm); err != nil {
			if strings.Contains(err.Error(), "405") {
				continue
			}
			return err
		}
	}
	return nil
}

// Remove deletes a file or collection.
func (f *FS) Remove(name string) error {
	if vfs.Clean(name) == "/" {
		return &fs.PathError{Op: "remove", Path: name, Err: vfs.ErrUnsupported}
	}
	ctx := context.Background()
	resp, err := f.do(ctx, http.MethodDelete, f.pathOf(name), nil, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webdav: delete %s: %s", name, resp.Status)
	}
	return nil
}

// RemoveAll is Remove: WebDAV deletes collections recursively by definition. A
// path that is already gone is not an error, which is what makes it safe after a
// partial failure.
func (f *FS) RemoveAll(name string) error {
	err := f.Remove(name)
	if errors.Is(err, vfs.ErrNotExist) {
		return nil
	}
	return err
}

// Rename is one MOVE — the real thing, not copy-then-delete.
func (f *FS) Rename(oldpath, newpath string) error {
	ctx := context.Background()
	resp, err := f.do(ctx, "MOVE", f.pathOf(oldpath), nil, map[string]string{
		"Destination": f.pathOf(newpath),
		"Overwrite":   "T",
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webdav: move %s: %s", oldpath, resp.Status)
	}
	return nil
}

// Chmod is not supported: WebDAV has no permission bits.
func (f *FS) Chmod(string, fs.FileMode) error { return vfs.ErrUnsupported }

// Chtimes is not supported: modification time belongs to the server.
func (f *FS) Chtimes(string, time.Time, time.Time) error { return vfs.ErrUnsupported }

// Readlink is not supported: WebDAV has no symlinks.
func (f *FS) Readlink(string) (string, error) { return "", vfs.ErrUnsupported }

// Symlink is not supported: WebDAV has no symlinks.
func (f *FS) Symlink(string, string) error { return vfs.ErrUnsupported }

// Truncate rewrites the file, because WebDAV has no truncate: read it, cut or pad
// it, put it back. It is the one operation here that costs the whole file.
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
	resp, err := f.do(context.Background(), http.MethodPut, f.pathOf(name), bytes.NewReader(data),
		map[string]string{"Content-Type": "application/octet-stream"})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webdav: put %s: %s", name, resp.Status)
	}
	return nil
}

// fileInfo is what a WebDAV answer can say.
type fileInfo struct {
	name     string
	size     int64
	modified time.Time
	dir      bool
}

func (i fileInfo) Name() string { return i.name }
func (i fileInfo) Size() int64  { return i.size }
func (i fileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (i fileInfo) ModTime() time.Time { return i.modified }
func (i fileInfo) IsDir() bool        { return i.dir }
func (i fileInfo) Sys() any           { return nil }

type dirEntry struct{ info fileInfo }

func (e dirEntry) Name() string               { return e.info.name }
func (e dirEntry) IsDir() bool                { return e.info.dir }
func (e dirEntry) Type() fs.FileMode          { return e.info.Mode().Type() }
func (e dirEntry) Info() (fs.FileInfo, error) { return e.info, nil }

// remoteFile is one open GET.
type remoteFile struct {
	name string
	body io.ReadCloser
	info fileInfo
}

func (f *remoteFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *remoteFile) Read(p []byte) (int, error) { return f.body.Read(p) }
func (f *remoteFile) Close() error               { return f.body.Close() }
func (f *remoteFile) ReadDir(int) ([]fs.DirEntry, error) {
	return nil, &fs.PathError{Op: "readdir", Path: f.name, Err: fs.ErrInvalid}
}

// upload buffers a write and PUTs it on Close.
type upload struct {
	fs     *FS
	name   string
	tmp    *os.File
	closed bool
}

func (u *upload) Write(p []byte) (int, error)         { return u.tmp.Write(p) }
func (u *upload) ReadFrom(r io.Reader) (int64, error) { return io.Copy(u.tmp, r) }
func (u *upload) Sync() error                         { return u.tmp.Sync() }

func (u *upload) Close() error {
	if u.closed {
		return nil
	}
	u.closed = true
	name := u.tmp.Name()
	defer func() { _ = os.Remove(name) }()

	info, err := u.tmp.Stat()
	if err != nil {
		_ = u.tmp.Close()
		return err
	}
	if _, err := u.tmp.Seek(0, io.SeekStart); err != nil {
		_ = u.tmp.Close()
		return err
	}
	defer func() { _ = u.tmp.Close() }()

	// A buffer of the size the request needs: http.NewRequest with an *os.File
	// cannot know the length, and every WebDAV server wants it.
	body, err := io.ReadAll(io.LimitReader(u.tmp, info.Size()))
	if err != nil {
		return err
	}
	resp, err := u.fs.do(context.Background(), http.MethodPut, u.fs.pathOf(u.name),
		bytes.NewReader(body), map[string]string{"Content-Type": "application/octet-stream"})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webdav: put %s: %s", u.name, resp.Status)
	}
	return nil
}
