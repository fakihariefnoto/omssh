package azure

import (
	"context"
	"io"
	"io/fs"
	"os"
	"time"
)

// fileInfo is what blob storage can honestly say about an entry.
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

// dirEntry adapts fileInfo to fs.DirEntry.
type dirEntry struct{ info fileInfo }

func (e dirEntry) Name() string               { return e.info.name }
func (e dirEntry) IsDir() bool                { return e.info.dir }
func (e dirEntry) Type() fs.FileMode          { return e.info.Mode().Type() }
func (e dirEntry) Info() (fs.FileInfo, error) { return e.info, nil }

// blobFile is one open blob, read-only.
type blobFile struct {
	name string
	body io.ReadCloser
	info fileInfo
}

func (f *blobFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *blobFile) Read(p []byte) (int, error) { return f.body.Read(p) }
func (f *blobFile) Close() error               { return f.body.Close() }
func (f *blobFile) ReadDir(int) ([]fs.DirEntry, error) {
	return nil, &fs.PathError{Op: "readdir", Path: f.name, Err: fs.ErrInvalid}
}

// WriterFrom lets the copy engine use the buffer pool's fast path.
func (f *blobFile) WriteTo(w io.Writer) (int64, error) { return io.Copy(w, f.body) }

// upload buffers a write locally and uploads on Close. Blob storage cannot
// append, and the SDK wants a size it can trust for one PUT, so the temp file is
// where both are learned.
type upload struct {
	ctx    context.Context
	api    api
	key    string
	tmp    *os.File
	closed bool
}

func (u *upload) Write(p []byte) (int, error) { return u.tmp.Write(p) }

// ReadFrom lets the copy engine use the fastest path the temp file offers.
func (u *upload) ReadFrom(r io.Reader) (int64, error) { return io.Copy(u.tmp, r) }

// Sync flushes the local buffer. The blob is durable once Close returns.
func (u *upload) Sync() error { return u.tmp.Sync() }

func (u *upload) Close() error {
	if u.closed {
		return nil
	}
	u.closed = true
	defer func() { _ = os.Remove(u.tmp.Name()) }()

	if _, err := u.tmp.Seek(0, io.SeekStart); err != nil {
		_ = u.tmp.Close()
		return err
	}
	defer func() { _ = u.tmp.Close() }()
	return u.api.Put(u.ctx, u.key, u.tmp)
}
