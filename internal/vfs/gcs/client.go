package gcs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// scope is the narrowest permission a file manager needs: read and write
// objects, not IAM or bucket administration.
const scope = "https://www.googleapis.com/auth/devstorage.read_write"

// meta is what GCS can say about one entry.
type meta struct {
	key      string
	size     int64
	modified time.Time
	dir      bool
}

// api is the narrow surface the filesystem needs. It exists so the conformance
// suite can run against a bucket in memory, and so the SDK stays in this file.
type api interface {
	Stat(ctx context.Context, key string) (meta, error)
	List(ctx context.Context, prefix, delimiter string) ([]meta, error)
	Open(ctx context.Context, key string) (io.ReadCloser, meta, error)
	Put(ctx context.Context, key string, body io.Reader) error
	Delete(ctx context.Context, key string) error
	// Copy is GCS's server-side copy: the bytes never pass through the client.
	Copy(ctx context.Context, source, target string) error
}

// Auth modes for the native client. "s3" is handled by the object package, not
// here; it stays a valid profile value because HMAC keys are a real answer.
const (
	AuthADC            = "adc"
	AuthServiceAccount = "service_account"
	AuthImpersonate    = "impersonate"
)

// AuthModes is the list the form and the validator agree on.
func AuthModes() []string { return []string{"s3", AuthADC, AuthServiceAccount, AuthImpersonate} }

// newAPI builds the client for one bucket.
func newAPI(ctx context.Context, opts Options) (api, error) {
	var clientOptions []option.ClientOption
	switch opts.Auth {
	case AuthADC:
		// Nothing: Application Default Credentials are the point.
	case AuthServiceAccount:
		switch {
		case strings.TrimSpace(opts.CredentialsFile) != "":
			clientOptions = append(clientOptions, option.WithAuthCredentialsFile(option.ServiceAccount, opts.CredentialsFile))
		case strings.TrimSpace(opts.CredentialsJSON) != "":
			clientOptions = append(clientOptions, option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(opts.CredentialsJSON)))
		default:
			return nil, errors.New("gcs: service_account needs a credentials file or stored JSON")
		}
	case AuthImpersonate:
		if strings.TrimSpace(opts.ServiceAccount) == "" {
			return nil, errors.New("gcs: impersonate needs the service account email to become")
		}
		// The caller proves who it is with ADC, then acts as the target account.
		source, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: opts.ServiceAccount,
			Scopes:          []string{scope},
		})
		if err != nil {
			return nil, err
		}
		clientOptions = append(clientOptions, option.WithTokenSource(source))
	default:
		return nil, errors.New("gcs: auth must be adc, service_account or impersonate")
	}

	client, err := storage.NewClient(ctx, clientOptions...)
	if err != nil {
		return nil, err
	}
	return &gcsAPI{bucket: client.Bucket(opts.Bucket)}, nil
}

// gcsAPI is the SDK behind the api interface.
type gcsAPI struct{ bucket *storage.BucketHandle }

func (a *gcsAPI) Stat(ctx context.Context, key string) (meta, error) {
	attrs, err := a.bucket.Object(key).Attrs(ctx)
	if err != nil {
		return meta{}, err
	}
	return meta{key: key, size: attrs.Size, modified: attrs.Updated,
		dir: strings.HasSuffix(key, "/") && attrs.Size == 0}, nil
}

func (a *gcsAPI) List(ctx context.Context, prefix, delimiter string) ([]meta, error) {
	it := a.bucket.Objects(ctx, &storage.Query{Prefix: prefix, Delimiter: delimiter})
	out := make([]meta, 0, 32)
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		if attrs.Prefix != "" {
			out = append(out, meta{key: attrs.Prefix, dir: true})
			continue
		}
		out = append(out, meta{key: attrs.Name, size: attrs.Size, modified: attrs.Updated,
			dir: strings.HasSuffix(attrs.Name, "/") && attrs.Size == 0})
	}
	return out, nil
}

func (a *gcsAPI) Open(ctx context.Context, key string) (io.ReadCloser, meta, error) {
	reader, err := a.bucket.Object(key).NewReader(ctx)
	if err != nil {
		return nil, meta{}, err
	}
	info := reader.Attrs
	return reader, meta{key: key, size: info.Size, modified: info.LastModified}, nil
}

func (a *gcsAPI) Put(ctx context.Context, key string, body io.Reader) error {
	// A writer commits on Close. Cancelling its context is how a failed copy
	// aborts the upload without leaving a partial object behind.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	writer := a.bucket.Object(key).NewWriter(ctx)
	if _, err := io.Copy(writer, body); err != nil {
		cancel()
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

func (a *gcsAPI) Delete(ctx context.Context, key string) error {
	return a.bucket.Object(key).Delete(ctx)
}

func (a *gcsAPI) Copy(ctx context.Context, source, target string) error {
	_, err := a.bucket.Object(target).CopierFrom(a.bucket.Object(source)).Run(ctx)
	return err
}

// classify turns an SDK error into the sentinel the interface branches on.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, storage.ErrObjectNotExist) || errors.Is(err, storage.ErrBucketNotExist) {
		return vfs.ErrNotExist
	}
	var apiError *googleapi.Error
	if errors.As(err, &apiError) && (apiError.Code == http.StatusForbidden || apiError.Code == http.StatusUnauthorized) {
		return vfs.ErrPermission
	}
	return err
}
