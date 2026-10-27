// Package cloud turns a configured profile into a filesystem. It is the one place
// that knows which provider needs which client, so the interface, the CLI and any
// future agent interface open a bucket the same way.
package cloud

import (
	"context"
	"errors"
	"strings"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/vfs"
	"github.com/fakihariefnoto/omssh/internal/vfs/azure"
	"github.com/fakihariefnoto/omssh/internal/vfs/gcs"
	"github.com/fakihariefnoto/omssh/internal/vfs/object"
	"github.com/fakihariefnoto/omssh/internal/vfs/webdav"
)

// Credentials are the secret half of a profile, resolved by the caller (keyring,
// agent, environment) so this package never touches storage.
type Credentials struct {
	// AccessKey and Secret are the S3 pair, when one is stored.
	AccessKey, Secret string
	// Username and Password are the WebDAV pair, when one is stored.
	Username, Password string
	// Secret is the one value some providers keep: an Azure account key or SAS
	// token. Which one it is comes from the profile's auth mode.
}

// Open builds the filesystem a profile describes.
func Open(ctx context.Context, name string, profile config.CloudProfile, creds Credentials) (vfs.FS, error) {
	provider := strings.ToLower(strings.TrimSpace(profile.Provider))
	if provider == "gcs" {
		switch strings.ToLower(strings.TrimSpace(profile.Auth)) {
		case "", "s3":
			// HMAC keys through the S3-compatible XML API, the original path.
		default:
			opts := gcs.Options{
				Bucket:          profile.Bucket,
				Prefix:          profile.Prefix,
				Auth:            profile.Auth,
				CredentialsFile: profile.CredentialsFile,
				ServiceAccount:  profile.ServiceAccount,
				Label:           name,
			}
			// A service-account document kept in the keyring comes through the
			// secret, exactly like every other provider's one secret.
			if strings.EqualFold(opts.Auth, gcs.AuthServiceAccount) && strings.TrimSpace(opts.CredentialsFile) == "" {
				opts.CredentialsJSON = creds.Secret
			}
			return gcs.New(opts)
		}
	}
	if provider == "azure" {
		opts := azure.Options{
			Account:   profile.Account,
			Container: profile.Bucket,
			Prefix:    profile.Prefix,
			Endpoint:  profile.Endpoint,
			Auth:      profile.Auth,
			Label:     name,
		}
		// The keyring holds one secret per profile; which kind it is comes from
		// the profile, so a key is never offered where a SAS token belongs.
		switch strings.ToLower(strings.TrimSpace(profile.Auth)) {
		case azure.AuthKey, "":
			if strings.TrimSpace(profile.Auth) == "" {
				opts.Auth = azure.AuthEntra
				break
			}
			opts.Key = creds.Secret
		case azure.AuthSAS:
			opts.SAS = creds.Secret
		}
		return azure.New(opts)
	}
	if provider == "webdav" {
		return webdav.New(webdav.Options{
			BaseURL:  profile.Endpoint,
			Username: creds.Username,
			Password: creds.Password,
			Label:    name,
		})
	}
	client, err := object.NewClient(ctx, object.ClientOptions{
		Provider:        provider,
		Region:          profile.Region,
		Endpoint:        profile.Endpoint,
		PathStyle:       profile.PathStyle,
		AccessKeyID:     creds.AccessKey,
		SecretAccessKey: creds.Secret,
	})
	if err != nil {
		return nil, err
	}
	return object.New(object.Options{API: client, Bucket: profile.Bucket, Prefix: profile.Prefix, Label: name})
}

// IsWebDAV reports whether a profile is a WebDAV server rather than a bucket.
func IsWebDAV(profile config.CloudProfile) bool {
	return strings.ToLower(strings.TrimSpace(profile.Provider)) == "webdav"
}

// Validate is the profile half of the config check, kept here so the form and the
// config agree about what a provider needs.
func Validate(profile config.CloudProfile) error {
	switch strings.ToLower(strings.TrimSpace(profile.Provider)) {
	case "webdav":
		if strings.TrimSpace(profile.Endpoint) == "" {
			return errors.New("WebDAV needs a URL, like https://cloud.example.com/remote.php/dav/files/me/")
		}
		if !strings.HasPrefix(profile.Endpoint, "http://") && !strings.HasPrefix(profile.Endpoint, "https://") {
			return errors.New("the WebDAV URL must start with http:// or https://")
		}
		return nil
	case "azure":
		if strings.TrimSpace(profile.Bucket) == "" {
			return errors.New("an Azure container name is required")
		}
		return nil
	case "gcs":
		if strings.TrimSpace(profile.Bucket) == "" {
			return errors.New("a bucket name is required")
		}
		switch strings.ToLower(strings.TrimSpace(profile.Auth)) {
		case "", "s3", "adc", "service_account":
		case "impersonate":
			if strings.TrimSpace(profile.ServiceAccount) == "" {
				return errors.New("impersonation needs the service account to become")
			}
		default:
			return errors.New("GCS auth must be s3, adc, service_account or impersonate")
		}
		return nil
	case "s3", "r2", "b2", "oss":
		if strings.TrimSpace(profile.Bucket) == "" {
			return errors.New("a bucket name is required")
		}
		return nil
	default:
		return errors.New("unknown provider " + profile.Provider)
	}
}
