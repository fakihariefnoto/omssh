package object

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// Metadata comes from HEAD, not the listing, and a filesystem without it simply
// does not implement the interface.
func TestMetadata(t *testing.T) {
	fsys, _ := testFS(t)
	meta, err := fsys.Metadata("/docs/readme.txt")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Size != 5 {
		t.Errorf("Size = %d, want 5", meta.Size)
	}
	if meta.Modified.IsZero() {
		t.Error("modified time should come from HEAD")
	}
	var _ vfs.Metadataer = fsys
}

// A temporary URL is signed for the object's full key, prefix included.
func TestPresignGet(t *testing.T) {
	var asked string
	fsys, err := New(Options{
		API: newFake(), Bucket: "bucket", Prefix: "site", Label: "test",
		Presign: func(_ context.Context, key string, expiry time.Duration) (string, error) {
			asked = key + "|" + expiry.String()
			return "https://example.test/signed", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	url, err := fsys.PresignGet("/css/app.css", 15*time.Minute)
	if err != nil || url != "https://example.test/signed" {
		t.Fatalf("PresignGet = %q, %v", url, err)
	}
	if asked != "site/css/app.css|15m0s" {
		t.Errorf("signed key = %q, want the prefixed key", asked)
	}

	plain, _ := testFS(t)
	if _, err := plain.PresignGet("/docs/readme.txt", time.Minute); !errors.Is(err, vfs.ErrUnsupported) {
		t.Errorf("a client without a signer = %v, want ErrUnsupported", err)
	}
}

// Server-side copy works inside one bucket and refuses another filesystem, whose
// bytes would need a different authorization.
func TestCopyRemote(t *testing.T) {
	fsys, api := testFS(t)
	if err := fsys.CopyRemote(fsys, "/docs/readme.txt", "/docs/copy.txt"); err != nil {
		t.Fatal(err)
	}
	if got := string(api.objects["docs/copy.txt"]); got != "hello" {
		t.Errorf("copied body = %q", got)
	}

	other, _ := testFS(t)
	err := fsys.CopyRemote(other, "/docs/readme.txt", "/docs/nope.txt")
	if !errors.Is(err, vfs.ErrUnsupported) {
		t.Errorf("a copy from another bucket = %v, want ErrUnsupported", err)
	}
}
