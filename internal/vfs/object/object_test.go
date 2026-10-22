package object

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// fakeAPI is a bucket in memory, with just enough of the S3 surface for the
// filesystem: prefix listing with a delimiter, get, head, put, copy, delete.
type fakeAPI struct {
	objects map[string][]byte
	times   map[string]time.Time
}

func newFake() *fakeAPI {
	return &fakeAPI{objects: map[string][]byte{}, times: map[string]time.Time{}}
}

func (f *fakeAPI) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	prefix := aws.ToString(in.Prefix)
	out := &s3.ListObjectsV2Output{}
	seenDirs := map[string]bool{}
	keys := make([]string, 0, len(f.objects))
	for key := range f.objects {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		if in.Delimiter != nil && *in.Delimiter == "/" {
			if i := strings.Index(rest, "/"); i >= 0 {
				dir := prefix + rest[:i+1]
				if !seenDirs[dir] {
					seenDirs[dir] = true
					out.CommonPrefixes = append(out.CommonPrefixes, types.CommonPrefix{Prefix: aws.String(dir)})
				}
				continue
			}
		}
		size := int64(len(f.objects[key]))
		modified := f.times[key]
		out.Contents = append(out.Contents, types.Object{Key: aws.String(key), Size: aws.Int64(size), LastModified: aws.Time(modified)})
		if in.MaxKeys != nil && len(out.Contents)+len(out.CommonPrefixes) >= int(*in.MaxKeys) {
			break
		}
	}
	out.IsTruncated = aws.Bool(false)
	return out, nil
}

func (f *fakeAPI) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := aws.ToString(in.Key)
	data, ok := f.objects[key]
	if !ok {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey", Message: key}
	}
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(strings.NewReader(string(data))),
		ContentLength: aws.Int64(int64(len(data))),
		LastModified:  aws.Time(f.times[key]),
	}, nil
}

func (f *fakeAPI) HeadObject(_ context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	key := aws.ToString(in.Key)
	data, ok := f.objects[key]
	if !ok {
		return nil, &smithy.GenericAPIError{Code: "NotFound", Message: key}
	}
	return &s3.HeadObjectOutput{ContentLength: aws.Int64(int64(len(data))), LastModified: aws.Time(f.times[key])}, nil
}

func (f *fakeAPI) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	key := aws.ToString(in.Key)
	f.objects[key] = body
	f.times[key] = time.Now()
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeAPI) CopyObject(_ context.Context, in *s3.CopyObjectInput, _ ...func(*s3.Options)) (*s3.CopyObjectOutput, error) {
	source := strings.TrimPrefix(aws.ToString(in.CopySource), "bucket/")
	data, ok := f.objects[source]
	if !ok {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey"}
	}
	key := aws.ToString(in.Key)
	f.objects[key] = append([]byte(nil), data...)
	f.times[key] = time.Now()
	return &s3.CopyObjectOutput{}, nil
}

func (f *fakeAPI) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	key := aws.ToString(in.Key)
	delete(f.objects, key)
	return &s3.DeleteObjectOutput{}, nil
}

func (f *fakeAPI) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	for _, id := range in.Delete.Objects {
		delete(f.objects, aws.ToString(id.Key))
	}
	return &s3.DeleteObjectsOutput{}, nil
}

func testFS(t *testing.T) (*FS, *fakeAPI) {
	t.Helper()
	api := newFake()
	api.objects["docs/readme.txt"] = []byte("hello")
	api.objects["docs/guide.md"] = []byte("guide")
	api.objects["top.txt"] = []byte("top")
	api.objects["empty/"] = []byte("")
	for key := range api.objects {
		api.times[key] = time.Now()
	}
	fsys, err := New(Options{API: api, Bucket: "bucket", Label: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return fsys, api
}

func TestReadDirListsFoldersAndFiles(t *testing.T) {
	fsys, _ := testFS(t)
	entries, err := fsys.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()+map[bool]string{true: "/", false: ""}[e.IsDir()]] = true
	}
	for _, want := range []string{"docs/", "empty/", "top.txt"} {
		if !got[want] {
			t.Errorf("root should list %q, got %v", want, got)
		}
	}
	// A directory marker is a folder, not a file called "empty".
	for _, e := range entries {
		if e.Name() == "empty" && !e.IsDir() {
			t.Error("a directory marker should be a folder")
		}
	}
}

func TestStatKnowsFoldersFromPrefixes(t *testing.T) {
	fsys, _ := testFS(t)
	info, err := fsys.Stat("/docs")
	if err != nil || !info.IsDir() {
		t.Fatalf("docs is a prefix and should stat as a folder: %v %v", info, err)
	}
	if _, err := fsys.Stat("/nope"); !errors.Is(err, vfs.ErrNotExist) {
		t.Errorf("a missing path should be ErrNotExist, got %v", err)
	}
	if _, err := fsys.Stat("/top.txt"); err != nil {
		t.Errorf("a file should stat: %v", err)
	}
}

func TestReadWriteRenameRemove(t *testing.T) {
	fsys, api := testFS(t)

	data, err := fsys.ReadFile("/top.txt")
	if err != nil || string(data) != "top" {
		t.Fatalf("read: %q %v", data, err)
	}

	if err := fsys.Mkdir("/new", 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create("/new/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("written")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if string(api.objects["new/file.txt"]) != "written" {
		t.Fatalf("the object should be in the bucket, got %q", api.objects["new/file.txt"])
	}

	if err := fsys.Rename("/new/file.txt", "/new/moved.txt"); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.objects["new/file.txt"]; ok {
		t.Error("rename should remove the source")
	}
	if string(api.objects["new/moved.txt"]) != "written" {
		t.Error("rename should copy the bytes")
	}

	if err := fsys.Remove("/new/moved.txt"); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.objects["new/moved.txt"]; ok {
		t.Error("remove should delete the object")
	}

	// RemoveAll takes the whole prefix.
	if err := fsys.RemoveAll("/docs"); err != nil {
		t.Fatal(err)
	}
	for key := range api.objects {
		if strings.HasPrefix(key, "docs/") {
			t.Errorf("RemoveAll left %q", key)
		}
	}
}

func TestPrefixRootsTheFilesystem(t *testing.T) {
	api := newFake()
	api.objects["site/index.html"] = []byte("x")
	api.objects["site/assets/app.js"] = []byte("y")
	api.objects["other/secret.txt"] = []byte("z")
	fsys, err := New(Options{API: api, Bucket: "bucket", Prefix: "site"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.ReadFile("/index.html"); err != nil {
		t.Errorf("the prefix should be the root: %v", err)
	}
	entries, err := fsys.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "secret.txt" || entry.Name() == "other" {
			t.Error("a prefix must not expose the rest of the bucket")
		}
	}
	if fsys.String() != "s3://bucket/site" {
		t.Errorf("String = %q", fsys.String())
	}
}

func TestCapabilitiesAreHonest(t *testing.T) {
	fsys, _ := testFS(t)
	caps := fsys.Capabilities()
	if caps.Chmod || caps.Symlink || caps.Chtimes {
		t.Errorf("object storage has no modes, links or times: %+v", caps)
	}
	if !caps.ServerSideCopy {
		t.Error("copy-then-delete is a server-side copy; it should say so")
	}
	if err := fsys.Chmod("/top.txt", fs.FileMode(0o600)); !errors.Is(err, vfs.ErrUnsupported) {
		t.Errorf("Chmod should be ErrUnsupported, got %v", err)
	}
	if err := fsys.Symlink("a", "b"); !errors.Is(err, vfs.ErrUnsupported) {
		t.Errorf("Symlink should be ErrUnsupported, got %v", err)
	}
}
