package cloud

import (
	"context"
	"errors"
	"testing"

	"github.com/fakihariefnoto/omssh/internal/config"
)

// A WebDAV profile becomes a WebDAV filesystem, not an S3 client.
func TestOpenWebDAV(t *testing.T) {
	fsys, err := Open(context.Background(), "nextcloud", config.CloudProfile{
		Provider: "webdav",
		Endpoint: "https://cloud.example.com/remote.php/dav/files/me/",
	}, Credentials{Username: "me", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if fsys.String() != "webdav:cloud.example.com/remote.php/dav/files/me/" {
		t.Errorf("String = %q", fsys.String())
	}
	if fsys.Capabilities().Symlink || fsys.Capabilities().Chmod {
		t.Error("WebDAV has no links or modes")
	}
	if !fsys.Capabilities().ServerSideCopy {
		t.Error("a rename is one MOVE: server-side copy should be advertised")
	}
}

// A bucket profile becomes an object filesystem, and the provider's endpoint is
// expected rather than the client's.
func TestOpenBucket(t *testing.T) {
	fsys, err := Open(context.Background(), "b2", config.CloudProfile{
		Provider: "b2", Bucket: "archive", Region: "us-west-004",
	}, Credentials{AccessKey: "id", Secret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if fsys.String() != "s3://archive" {
		t.Errorf("String = %q", fsys.String())
	}
}

// Azure is a container, not a bucket, and the auth mode decides which secret the
// keyring's one value is.
func TestOpenAzure(t *testing.T) {
	fsys, err := Open(context.Background(), "blobs", config.CloudProfile{
		Provider: "azure", Account: "acct", Bucket: "site", Auth: "key",
	}, Credentials{Secret: "a2V5"})
	if err != nil {
		t.Fatal(err)
	}
	if fsys.String() != "azure:site/" {
		t.Errorf("String = %q", fsys.String())
	}
	if fsys.Capabilities().Chmod || fsys.Capabilities().Symlink {
		t.Error("blobs have no modes and no links")
	}

	// Entra needs nothing from the keyring, so an empty secret is fine; an empty
	// key is not when key auth was chosen.
	if _, err := Open(context.Background(), "blobs", config.CloudProfile{
		Provider: "azure", Account: "acct", Bucket: "site", Auth: "key",
	}, Credentials{}); err == nil {
		t.Error("key auth without a stored key should fail")
	}
}

// The validation the form and the config share.
func TestValidate(t *testing.T) {
	if err := Validate(config.CloudProfile{Provider: "webdav"}); err == nil {
		t.Error("a WebDAV profile needs a URL")
	}
	if err := Validate(config.CloudProfile{Provider: "webdav", Endpoint: "ftp://x"}); err == nil {
		t.Error("the URL must be http or https")
	}
	if err := Validate(config.CloudProfile{Provider: "webdav", Endpoint: "https://x/dav/"}); err != nil {
		t.Errorf("a valid WebDAV profile was refused: %v", err)
	}
	if err := Validate(config.CloudProfile{Provider: "s3"}); err == nil {
		t.Error("a bucket provider needs a bucket")
	}
	if err := Validate(config.CloudProfile{Provider: "azure"}); err == nil {
		t.Error("Azure needs a container")
	}
	if err := Validate(config.CloudProfile{Provider: "azure", Bucket: "site"}); err != nil {
		t.Errorf("a valid Azure profile was refused: %v", err)
	}
	if err := Validate(config.CloudProfile{Provider: "nope"}); err == nil {
		t.Error("an unknown provider should be refused")
	}
	_ = errors.New
}
