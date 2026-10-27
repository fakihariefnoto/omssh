package azure

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// blobMeta is what blob storage can say about one entry.
type blobMeta struct {
	key      string
	size     int64
	modified time.Time
	dir      bool
}

// api is the narrow surface the filesystem needs. It is an interface so the
// conformance suite can run against a container in memory, with no account and
// no network, and so the SDK stays in this file.
type api interface {
	// Stat asks about one blob.
	Stat(ctx context.Context, key string) (blobMeta, error)
	// List returns the entries under a prefix. A "/" delimiter means one level,
	// where prefixes become directories; the empty delimiter means everything.
	List(ctx context.Context, prefix, delimiter string) ([]blobMeta, error)
	// Open starts a download.
	Open(ctx context.Context, key string) (io.ReadCloser, int64, error)
	// Put uploads a whole blob.
	Put(ctx context.Context, key string, body io.Reader) error
	// Delete removes one blob.
	Delete(ctx context.Context, key string) error
}

// Auth modes. They are strings in the config so a profile stays readable.
const (
	AuthKey   = "key"
	AuthSAS   = "sas"
	AuthEntra = "entra"
)

// AuthModes is the list the form and the validator agree on.
func AuthModes() []string { return []string{AuthKey, AuthSAS, AuthEntra} }

// newAPI builds the real client for one account and container.
func newAPI(opts Options) (api, error) {
	serviceURL := strings.TrimSpace(opts.Endpoint)
	if serviceURL == "" {
		serviceURL = "https://" + opts.Account + ".blob.core.windows.net/"
	}
	if !strings.HasSuffix(serviceURL, "/") {
		serviceURL += "/"
	}

	var client *azblob.Client
	var err error
	switch opts.Auth {
	case AuthKey:
		var cred *azblob.SharedKeyCredential
		cred, err = azblob.NewSharedKeyCredential(opts.Account, opts.Key)
		if err != nil {
			return nil, err
		}
		client, err = azblob.NewClientWithSharedKeyCredential(serviceURL, cred, nil)
	case AuthSAS:
		token := strings.TrimPrefix(strings.TrimSpace(opts.SAS), "?")
		if token == "" {
			return nil, errors.New("azure: a SAS token is required")
		}
		client, err = azblob.NewClientWithNoCredential(serviceURL+"?"+token, nil)
	case AuthEntra:
		var cred azcore.TokenCredential
		cred, err = azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, err
		}
		client, err = azblob.NewClient(serviceURL, cred, nil)
	default:
		return nil, errors.New("azure: auth must be key, sas or entra")
	}
	if err != nil {
		return nil, err
	}
	return &azureAPI{
		client:    client,
		container: client.ServiceClient().NewContainerClient(opts.Container),
		prefix:    normalizePrefix(opts.Prefix),
	}, nil
}

// azureAPI is the SDK behind the api interface.
type azureAPI struct {
	client    *azblob.Client
	container *container.Client
	prefix    string
}

func (a *azureAPI) Stat(ctx context.Context, key string) (blobMeta, error) {
	props, err := a.container.NewBlobClient(key).GetProperties(ctx, nil)
	if err != nil {
		return blobMeta{}, err
	}
	meta := blobMeta{key: key}
	if props.ContentLength != nil {
		meta.size = *props.ContentLength
	}
	if props.LastModified != nil {
		meta.modified = *props.LastModified
	}
	// A zero-byte blob whose name ends in "/" is a directory marker.
	meta.dir = strings.HasSuffix(key, "/") && meta.size == 0
	return meta, nil
}

func (a *azureAPI) List(ctx context.Context, prefix, delimiter string) ([]blobMeta, error) {
	options := &container.ListBlobsHierarchyOptions{}
	if prefix != "" {
		options.Prefix = &prefix
	}
	pager := a.container.NewListBlobsHierarchyPager(delimiter, options)
	out := make([]blobMeta, 0, 32)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		if page.Segment == nil {
			continue
		}
		for _, dir := range page.Segment.BlobPrefixes {
			if dir.Name == nil {
				continue
			}
			out = append(out, blobMeta{key: *dir.Name, dir: true})
		}
		for _, item := range page.Segment.BlobItems {
			if item.Name == nil {
				continue
			}
			meta := blobMeta{key: *item.Name}
			if item.Properties != nil {
				if item.Properties.ContentLength != nil {
					meta.size = *item.Properties.ContentLength
				}
				if item.Properties.LastModified != nil {
					meta.modified = *item.Properties.LastModified
				}
			}
			meta.dir = strings.HasSuffix(meta.key, "/") && meta.size == 0
			out = append(out, meta)
		}
	}
	return out, nil
}

func (a *azureAPI) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	response, err := a.container.NewBlobClient(key).DownloadStream(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	size := int64(0)
	if response.ContentLength != nil {
		size = *response.ContentLength
	}
	return response.Body, size, nil
}

func (a *azureAPI) Put(ctx context.Context, key string, body io.Reader) error {
	_, err := a.container.NewBlockBlobClient(key).UploadStream(ctx, body, nil)
	return err
}

func (a *azureAPI) Delete(ctx context.Context, key string) error {
	_, err := a.container.NewBlobClient(key).Delete(ctx, nil)
	return err
}

// classify turns an SDK error into the sentinel the interface branches on.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var response *azcore.ResponseError
	if errors.As(err, &response) {
		switch response.StatusCode {
		case http.StatusNotFound:
			return vfs.ErrNotExist
		case http.StatusForbidden, http.StatusUnauthorized:
			return vfs.ErrPermission
		}
	}
	return err
}
