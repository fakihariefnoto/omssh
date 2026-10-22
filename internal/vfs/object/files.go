package object

import (
	"context"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// fileInfo is what object storage can honestly say about an entry.
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

// objectFile is one open object, read-only.
type objectFile struct {
	name string
	body io.ReadCloser
	info fileInfo
}

func (f *objectFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *objectFile) Read(p []byte) (int, error) { return f.body.Read(p) }
func (f *objectFile) Close() error               { return f.body.Close() }
func (f *objectFile) ReadDir(int) ([]fs.DirEntry, error) {
	return nil, &fs.PathError{Op: "readdir", Path: f.name, Err: fs.ErrInvalid}
}

// upload buffers a write locally and puts it on Close. Object storage cannot
// append or seek, and the SDK needs a size it can trust for a single PUT, so the
// temp file is the honest place to learn both. Multipart uploads replace this for
// large objects in a later milestone.
type upload struct {
	ctx    context.Context
	api    API
	bucket string
	key    string
	tmp    *os.File
	closed bool
}

func (u *upload) Write(p []byte) (int, error) { return u.tmp.Write(p) }

// ReadFrom lets the copy engine use the fastest path the temp file offers.
func (u *upload) ReadFrom(r io.Reader) (int64, error) { return io.Copy(u.tmp, r) }

// Sync flushes the local buffer. The object itself is durable once Close returns.
func (u *upload) Sync() error { return u.tmp.Sync() }

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

	_, err = u.api.PutObject(u.ctx, &s3.PutObjectInput{
		Bucket:        aws.String(u.bucket),
		Key:           aws.String(u.key),
		Body:          u.tmp,
		ContentLength: aws.Int64(info.Size()),
	})
	return err
}
