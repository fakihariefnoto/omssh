// Package object is the vfs implementation for S3-compatible object storage:
// AWS S3, Cloudflare R2, Backblaze B2, Alibaba OSS and Google Cloud Storage's
// S3-compatible XML API.
//
// Object storage is not a filesystem, and pretending otherwise is how tools
// become liars. There are no directories (only keys that share a prefix), no
// permissions, no symlinks and no atomic rename. This implementation says so
// through Capabilities and returns ErrUnsupported for what it cannot do, so the
// interface can degrade instead of failing halfway through a copy.
package object

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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// API is the slice of the S3 client this package uses. Keeping it narrow is what
// makes the behaviour testable without a bucket.
type API interface {
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, opt ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	GetObject(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, opt ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	PutObject(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	CopyObject(ctx context.Context, in *s3.CopyObjectInput, opt ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
	DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, opt ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	DeleteObjects(ctx context.Context, in *s3.DeleteObjectsInput, opt ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

// Options configures one bucket.
type Options struct {
	// Bucket is required. Prefix roots the filesystem inside the bucket, which is
	// how "show me only this folder" works without a second mount.
	Bucket string
	// Prefix is optional and always treated as a directory: "site/" or "site".
	Prefix string
	// Label is the name shown in the interface: the profile name.
	Label string
	// API is the client. Required; NewClient builds one from the AWS
	// configuration.
	API API
}

// FS is one bucket (or one prefix inside it) as a filesystem.
type FS struct {
	api    API
	bucket string
	prefix string
	label  string
}

// New validates the options and returns the filesystem.
func New(opts Options) (*FS, error) {
	if opts.API == nil {
		return nil, errors.New("object: no client")
	}
	if strings.TrimSpace(opts.Bucket) == "" {
		return nil, errors.New("object: a bucket is required")
	}
	prefix := strings.Trim(strings.TrimSpace(opts.Prefix), "/")
	if prefix != "" {
		prefix += "/"
	}
	label := opts.Label
	if label == "" {
		label = opts.Bucket
	}
	return &FS{api: opts.API, bucket: opts.Bucket, prefix: prefix, label: label}, nil
}

// key turns a vfs path into an object key. The root is the prefix itself.
func (f *FS) key(name string) string {
	clean := strings.TrimPrefix(vfs.Clean(name), "/")
	if clean == "" {
		return f.prefix
	}
	return f.prefix + clean
}

// Join builds a path the way the interface expects: slash-separated and rooted.
func (f *FS) Join(elem ...string) string {
	return vfs.Clean(path.Join(elem...))
}

// Dir keeps the shape the caller gave it: the conformance suite asks for dir/sub
// from dir/sub/file, so forcing a leading slash here would be wrong.
func (f *FS) Dir(name string) string {
	if name == "" {
		return "."
	}
	return path.Dir(name)
}

// Base is the last element of a path.
func (f *FS) Base(name string) string { return path.Base(name) }

// Abs resolves a path against the root.
func (f *FS) Abs(name string) (string, error) { return vfs.Clean(name), nil }

// String names the filesystem for logs and error messages.
func (f *FS) String() string {
	if f.prefix == "" {
		return "s3://" + f.bucket
	}
	return "s3://" + f.bucket + "/" + strings.TrimSuffix(f.prefix, "/")
}

// Capabilities is the honest answer, not the optimistic one.
func (f *FS) Capabilities() vfs.Capabilities {
	return vfs.Capabilities{
		Symlink:        false,
		Chmod:          false,
		Chtimes:        false,
		CopyData:       false,
		ServerSideCopy: true,
		CaseSensitive:  true,
	}
}

// Open reads one object.
func (f *FS) Open(name string) (fs.File, error) {
	ctx := context.Background()
	out, err := f.api.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(f.bucket),
		Key:    aws.String(f.key(name)),
	})
	if err != nil {
		return nil, f.wrap(name, err)
	}
	size := int64(0)
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	modified := time.Time{}
	if out.LastModified != nil {
		modified = *out.LastModified
	}
	return &objectFile{
		name: path.Base(vfs.Clean(name)),
		body: out.Body,
		info: fileInfo{name: path.Base(vfs.Clean(name)), size: size, modified: modified},
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
	key := f.key(clean)
	out, err := f.api.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(f.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		size := int64(0)
		if out.ContentLength != nil {
			size = *out.ContentLength
		}
		modified := time.Time{}
		if out.LastModified != nil {
			modified = *out.LastModified
		}
		return fileInfo{name: path.Base(clean), size: size, modified: modified}, nil
	}
	if !isNotFound(err) {
		return nil, f.wrap(name, err)
	}
	// Not an object. It may still be a "directory": a prefix with objects under it.
	list, listErr := f.api.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(f.bucket),
		Prefix:  aws.String(key + "/"),
		MaxKeys: aws.Int32(1),
	})
	if listErr == nil && (len(list.Contents) > 0 || len(list.CommonPrefixes) > 0) {
		return fileInfo{name: path.Base(clean), dir: true}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: vfs.ErrNotExist}
}

// ReadDir lists one level. Directories are the common prefixes, which is the only
// notion of a folder object storage has.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	clean := vfs.Clean(name)
	prefix := f.key(clean)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	ctx := context.Background()
	entries := make([]fs.DirEntry, 0, 32)
	var token *string
	marker := false
	for {
		out, err := f.api.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(f.bucket),
			Prefix:            aws.String(prefix),
			Delimiter:         aws.String("/"),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, f.wrap(name, err)
		}
		for _, common := range out.CommonPrefixes {
			key := aws.ToString(common.Prefix)
			entries = append(entries, dirEntry{info: fileInfo{
				name: path.Base(strings.TrimSuffix(key, "/")),
				dir:  true,
			}})
		}
		for _, obj := range out.Contents {
			key := aws.ToString(obj.Key)
			if key == prefix || strings.HasSuffix(key, "/") {
				// A directory marker, not a file.
				marker = true
				continue
			}
			size := int64(0)
			if obj.Size != nil {
				size = *obj.Size
			}
			modified := time.Time{}
			if obj.LastModified != nil {
				modified = *obj.LastModified
			}
			entries = append(entries, dirEntry{info: fileInfo{
				name:     path.Base(key),
				size:     size,
				modified: modified,
			}})
		}
		if out.IsTruncated == nil || !*out.IsTruncated || out.NextContinuationToken == nil {
			break
		}
		token = out.NextContinuationToken
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if len(entries) == 0 && !marker && clean != "/" {
		// Nothing under the prefix and no marker: the directory does not exist.
		if _, err := f.Stat(clean); err != nil {
			return nil, &fs.PathError{Op: "readdir", Path: name, Err: vfs.ErrNotExist}
		}
	}
	return entries, nil
}

// Create makes or truncates an object.
func (f *FS) Create(name string) (vfs.WFile, error) {
	return f.OpenFile(name, vfs.O_WRONLY|vfs.O_CREATE|vfs.O_TRUNC, 0o644)
}

// OpenFile only supports writing: object storage has no append and no random
// write, so a flag combination that asks for either is refused rather than
// silently doing something else.
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
		return nil, fmt.Errorf("object: %w (no append)", vfs.ErrUnsupported)
	}
	if flag&os.O_EXCL != 0 {
		if _, err := f.Stat(name); err == nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: vfs.ErrExist}
		}
	}
	// A parent that does not exist is an error, as it is on a real filesystem:
	// object storage would happily invent the prefix, and a typo would become a
	// folder nobody meant to create.
	if parent := vfs.Clean(f.Dir(name)); parent != "/" && parent != "." {
		if _, err := f.Stat(parent); err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: vfs.ErrNotExist}
		}
	}
	file, err := os.CreateTemp("", "omssh-s3-*")
	if err != nil {
		return nil, err
	}
	return &upload{
		ctx:    context.Background(),
		api:    f.api,
		bucket: f.bucket,
		key:    f.key(name),
		tmp:    file,
	}, nil
}

// Mkdir writes a directory marker, which is how an empty "folder" exists in a
// bucket at all.
func (f *FS) Mkdir(name string, _ fs.FileMode) error {
	if clean := vfs.Clean(name); clean == "/" {
		return &fs.PathError{Op: "mkdir", Path: name, Err: vfs.ErrExist}
	}
	if _, err := f.Stat(name); err == nil {
		return &fs.PathError{Op: "mkdir", Path: name, Err: vfs.ErrExist}
	}
	key := strings.TrimSuffix(f.key(name), "/") + "/"
	_, err := f.api.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(f.bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(""),
	})
	if err != nil {
		return f.wrap(name, err)
	}
	return nil
}

// MkdirAll creates the final marker. The prefixes above it exist as soon as
// anything is written under them, so nothing else is needed.
func (f *FS) MkdirAll(name string, perm fs.FileMode) error {
	if _, err := f.Stat(name); err == nil {
		return nil
	}
	return f.Mkdir(name, perm)
}

// Remove deletes one object.
func (f *FS) Remove(name string) error {
	if vfs.Clean(name) == "/" {
		return &fs.PathError{Op: "remove", Path: name, Err: vfs.ErrUnsupported}
	}
	if _, err := f.Stat(name); err != nil {
		return &fs.PathError{Op: "remove", Path: name, Err: vfs.ErrNotExist}
	}
	_, err := f.api.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(f.bucket),
		Key:    aws.String(f.key(name)),
	})
	if err != nil {
		return f.wrap(name, err)
	}
	return nil
}

// RemoveAll deletes everything under a prefix, in batches of 1000.
func (f *FS) RemoveAll(name string) error {
	clean := vfs.Clean(name)
	if clean == "/" {
		return &fs.PathError{Op: "removeall", Path: name, Err: vfs.ErrUnsupported}
	}
	prefix := f.key(clean)
	ctx := context.Background()
	var token *string
	for {
		out, err := f.api.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(f.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return f.wrap(name, err)
		}
		if len(out.Contents) > 0 {
			ids := make([]types.ObjectIdentifier, 0, len(out.Contents))
			for _, obj := range out.Contents {
				ids = append(ids, types.ObjectIdentifier{Key: obj.Key})
			}
			if _, err := f.api.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(f.bucket),
				Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
			}); err != nil {
				return f.wrap(name, err)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated || out.NextContinuationToken == nil {
			break
		}
		token = out.NextContinuationToken
	}
	return nil
}

// Rename is copy-then-delete, because that is the only rename this storage has.
// It is not atomic, and a failure between the two leaves both copies.
func (f *FS) Rename(oldpath, newpath string) error {
	ctx := context.Background()
	source := f.bucket + "/" + f.key(oldpath)
	_, err := f.api.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(f.bucket),
		CopySource: aws.String(source),
		Key:        aws.String(f.key(newpath)),
	})
	if err != nil {
		return f.wrap(oldpath, err)
	}
	return f.Remove(oldpath)
}

// Chmod is not supported: objects have no permission bits.
func (f *FS) Chmod(string, fs.FileMode) error { return vfs.ErrUnsupported }

// Chtimes is not supported: modification time is set by the upload.
func (f *FS) Chtimes(string, time.Time, time.Time) error { return vfs.ErrUnsupported }

// Readlink is not supported: objects are not links.
func (f *FS) Readlink(string) (string, error) { return "", vfs.ErrUnsupported }

// Symlink is not supported: objects are not links.
func (f *FS) Symlink(string, string) error { return vfs.ErrUnsupported }

// Truncate rewrites the object: read it, cut or pad it, put it back. It is the
// one operation here that costs the whole file, which is why the interface rarely
// asks for it.
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
	_, err = f.api.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:        aws.String(f.bucket),
		Key:           aws.String(f.key(name)),
		Body:          strings.NewReader(string(data)),
		ContentLength: aws.Int64(int64(len(data))),
	})
	return err
}

// wrap turns an SDK error into the one the interface branches on: missing,
// refused, or something else with its own words.
func (f *FS) wrap(name string, err error) error {
	switch {
	case isNotFound(err):
		return &fs.PathError{Op: "open", Path: name, Err: vfs.ErrNotExist}
	case isDenied(err):
		return &fs.PathError{Op: "open", Path: name, Err: vfs.ErrPermission}
	default:
		return fmt.Errorf("%s: %w", f, err)
	}
}

func isNotFound(err error) bool {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound", "404", "NoSuchBucket":
			return true
		}
	}
	return errors.Is(err, fs.ErrNotExist)
}

func isDenied(err error) bool {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "AccessDenied", "Forbidden", "403", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return true
		}
	}
	return errors.Is(err, fs.ErrPermission)
}
