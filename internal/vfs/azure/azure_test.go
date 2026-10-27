package azure

import (
	"context"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fakihariefnoto/omssh/internal/vfs"
	"github.com/fakihariefnoto/omssh/internal/vfs/vfstest"
)

// fakeAPI is a container in memory: flat keys, and prefixes standing in for
// directories, which is all blob storage is.
type fakeAPI struct {
	objects map[string][]byte
	times   map[string]time.Time
}

func newFake() *fakeAPI {
	return &fakeAPI{objects: map[string][]byte{}, times: map[string]time.Time{}}
}

func (f *fakeAPI) Stat(_ context.Context, key string) (blobMeta, error) {
	body, ok := f.objects[key]
	if ok {
		return blobMeta{key: key, size: int64(len(body)), modified: f.times[key],
			dir: strings.HasSuffix(key, "/") && len(body) == 0}, nil
	}
	return blobMeta{}, vfs.ErrNotExist
}

func (f *fakeAPI) List(_ context.Context, prefix, delimiter string) ([]blobMeta, error) {
	keys := make([]string, 0, len(f.objects))
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := make([]blobMeta, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		rest := strings.TrimPrefix(key, prefix)
		if delimiter != "" {
			if i := strings.Index(rest, delimiter); i >= 0 {
				dir := prefix + rest[:i+1]
				if !seen[dir] {
					seen[dir] = true
					out = append(out, blobMeta{key: dir, dir: true})
				}
				continue
			}
		}
		out = append(out, blobMeta{key: key, size: int64(len(f.objects[key])), modified: f.times[key]})
	}
	return out, nil
}

func (f *fakeAPI) Open(_ context.Context, key string) (io.ReadCloser, int64, error) {
	body, ok := f.objects[key]
	if !ok {
		return nil, 0, vfs.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(string(body))), int64(len(body)), nil
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

func testFS(t *testing.T) *FS {
	t.Helper()
	fsys, err := New(Options{Account: "acct", Container: "site", Auth: AuthEntra, API: newFake(), Label: "azure"})
	if err != nil {
		t.Fatal(err)
	}
	return fsys
}

// The conformance suite every filesystem runs. Blob storage is allowed to fail
// the parts it declares it cannot do, but nothing else.
func TestConformance(t *testing.T) {
	vfstest.Run(t, testFS(t))
}

// A folder is a prefix, so renaming one means moving every blob under it.
func TestRenameDirectory(t *testing.T) {
	fsys := testFS(t)
	if err := fsys.MkdirAll("/one/two", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := vfs.WriteFile(fsys, "/one/two/a.txt", []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vfs.WriteFile(fsys, "/one/top.txt", []byte("top"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Rename("/one", "/moved"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	body, err := vfs.ReadAll(fsys, "/moved/two/a.txt")
	if err != nil || string(body) != "a" {
		t.Fatalf("the nested file did not move: %q, %v", body, err)
	}
	if _, err := fsys.Stat("/one"); err == nil {
		t.Error("the old prefix should be gone")
	}
}

// The profile decides how to authenticate, and each mode refuses to run without
// what it needs.
func TestAuthValidation(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"no container", Options{Account: "a", Auth: AuthEntra}, "container"},
		{"key without a key", Options{Account: "a", Container: "c", Auth: AuthKey}, "account key"},
		{"sas without a token", Options{Account: "a", Container: "c", Auth: AuthSAS}, "SAS"},
		{"unknown mode", Options{Account: "a", Container: "c", Auth: "password"}, "auth must be"},
		{"key without an account", Options{Container: "c", Auth: AuthKey, Key: "k"}, "account name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("New = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}

	// Entra needs no secret in the profile: the credential chain supplies it.
	if _, err := New(Options{Account: "a", Container: "c", Auth: AuthEntra, API: newFake()}); err != nil {
		t.Errorf("entra with a client: %v", err)
	}
}

// A prefix roots the filesystem, and nothing above it is reachable.
func TestPrefixRootsTheFilesystem(t *testing.T) {
	fake := newFake()
	fake.objects["site/css/app.css"] = []byte("body")
	fsys, err := New(Options{Account: "a", Container: "c", Auth: AuthEntra,
		Prefix: "site/", API: fake, Label: "site"})
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
	if _, err := fsys.Stat("/../secret"); err == nil {
		t.Error("a path above the prefix must not resolve")
	}
}
