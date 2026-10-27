package gcs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fakihariefnoto/omssh/internal/vfs"
	"github.com/fakihariefnoto/omssh/internal/vfs/vfstest"
)

// fakeAPI is a bucket in memory: flat keys, prefixes as directories, and a copy
// that stands in for GCS's server-side one.
type fakeAPI struct {
	objects map[string][]byte
	times   map[string]time.Time
}

func newFake() *fakeAPI {
	return &fakeAPI{objects: map[string][]byte{}, times: map[string]time.Time{}}
}

func (f *fakeAPI) Stat(_ context.Context, key string) (meta, error) {
	body, ok := f.objects[key]
	if !ok {
		return meta{}, vfs.ErrNotExist
	}
	return meta{key: key, size: int64(len(body)), modified: f.times[key],
		dir: strings.HasSuffix(key, "/") && len(body) == 0}, nil
}

func (f *fakeAPI) List(_ context.Context, prefix, delimiter string) ([]meta, error) {
	keys := make([]string, 0, len(f.objects))
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := make([]meta, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		rest := strings.TrimPrefix(key, prefix)
		if delimiter != "" {
			if i := strings.Index(rest, delimiter); i >= 0 {
				dir := prefix + rest[:i+1]
				if !seen[dir] {
					seen[dir] = true
					out = append(out, meta{key: dir, dir: true})
				}
				continue
			}
		}
		out = append(out, meta{key: key, size: int64(len(f.objects[key])), modified: f.times[key]})
	}
	return out, nil
}

func (f *fakeAPI) Open(_ context.Context, key string) (io.ReadCloser, meta, error) {
	body, ok := f.objects[key]
	if !ok {
		return nil, meta{}, vfs.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(string(body))),
		meta{key: key, size: int64(len(body)), modified: f.times[key]}, nil
}

func (f *fakeAPI) Put(_ context.Context, key string, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.objects[key] = data
	f.times[key] = time.Now()
	return nil
}

func (f *fakeAPI) Delete(_ context.Context, key string) error {
	if _, ok := f.objects[key]; !ok {
		return vfs.ErrNotExist
	}
	delete(f.objects, key)
	return nil
}

func (f *fakeAPI) Copy(_ context.Context, source, target string) error {
	body, ok := f.objects[source]
	if !ok {
		return vfs.ErrNotExist
	}
	data := append([]byte(nil), body...)
	f.objects[target] = data
	f.times[target] = time.Now()
	return nil
}

func testFS(t *testing.T) *FS {
	t.Helper()
	fsys, err := New(Options{Bucket: "site", Auth: AuthADC, API: newFake(), Label: "gcs"})
	if err != nil {
		t.Fatal(err)
	}
	return fsys
}

// The conformance suite every filesystem runs.
func TestConformance(t *testing.T) {
	vfstest.Run(t, testFS(t))
}

// A folder is a prefix, so renaming one copies every object under it, including
// the marker that makes an empty one exist.
func TestRenameDirectory(t *testing.T) {
	fsys := testFS(t)
	for _, dir := range []string{"/one/empty", "/one/two"} {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := vfs.WriteFile(fsys, "/one/two/a.txt", []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Rename("/one", "/moved"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	body, err := vfs.ReadAll(fsys, "/moved/two/a.txt")
	if err != nil || string(body) != "a" {
		t.Fatalf("the nested file did not move: %q, %v", body, err)
	}
	if info, err := fsys.Stat("/moved/empty"); err != nil || !info.IsDir() {
		t.Errorf("the empty folder should have moved: %v, %v", info, err)
	}
	if _, err := fsys.Stat("/one"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the old prefix should be gone, got %v", err)
	}
}

// Removing /site must not touch /site2, and an empty folder's marker must be
// deleted rather than called a non-empty directory.
func TestRemoveRespectsPathBoundaries(t *testing.T) {
	fsys := testFS(t)
	for _, dir := range []string{"/site", "/site2"} {
		if err := fsys.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"/site/a.txt", "/site2/keep.txt"} {
		if err := vfs.WriteFile(fsys, name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := fsys.RemoveAll("/site"); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := fsys.Stat("/site2/keep.txt"); err != nil {
		t.Errorf("a sibling sharing the prefix must survive: %v", err)
	}
	if err := fsys.Remove("/site2"); err == nil {
		t.Error("Remove of a non-empty folder should fail")
	}
	if err := fsys.Mkdir("/empty", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Remove("/empty"); err != nil {
		t.Fatalf("Remove of an empty folder: %v", err)
	}
}

// The native client needs one of three auth modes, and each says what it is
// missing rather than failing at the first API call.
func TestAuthValidation(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"no bucket", Options{Auth: AuthADC}, "bucket"},
		{"service account without credentials", Options{Bucket: "b", Auth: AuthServiceAccount}, "credentials"},
		{"impersonate without a target", Options{Bucket: "b", Auth: AuthImpersonate}, "service account"},
		{"unknown mode", Options{Bucket: "b", Auth: "hmac"}, "auth must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("New = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
	if _, err := New(Options{Bucket: "b", Auth: AuthADC, API: newFake()}); err != nil {
		t.Errorf("adc with a client: %v", err)
	}
}

// A prefix roots the filesystem, and nothing above it is reachable.
func TestPrefixRootsTheFilesystem(t *testing.T) {
	fake := newFake()
	fake.objects["site/css/app.css"] = []byte("body")
	fsys, err := New(Options{Bucket: "b", Auth: AuthADC, Prefix: "site/", API: fake, Label: "site"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fsys.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "css" || !entries[0].IsDir() {
		t.Fatalf("root = %+v, want one directory css", entries)
	}
	body, err := vfs.ReadAll(fsys, "/css/app.css")
	if err != nil || string(body) != "body" {
		t.Fatalf("read = %q, %v", body, err)
	}
}
