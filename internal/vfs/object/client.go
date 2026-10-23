package object

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Providers are the S3-compatible services the client knows how to address.
// They differ in endpoint, region and addressing style — configuration, not code.
var Providers = []string{"s3", "r2", "b2", "gcs", "oss"}

// ProviderNames is the list for the interface and for validation.
func ProviderNames() []string { return append([]string(nil), Providers...) }

// resolved is a profile after the provider's defaults are applied.
type resolved struct {
	region    string
	endpoint  string
	pathStyle bool
}

// resolve fills in what the provider implies: B2's endpoint is per region, GCS's
// is global, OSS is virtual-hosted only, R2 needs an account endpoint.
func resolve(opts ClientOptions) (resolved, error) {
	provider := strings.ToLower(strings.TrimSpace(opts.Provider))
	if provider == "" {
		provider = "s3"
	}
	out := resolved{region: strings.TrimSpace(opts.Region), endpoint: strings.TrimSpace(opts.Endpoint), pathStyle: opts.PathStyle}
	switch provider {
	case "s3":
		if out.region == "" {
			return out, errors.New("object: AWS S3 needs a region, like us-east-1")
		}
	case "r2":
		if out.region == "" {
			out.region = "auto"
		}
		if out.endpoint == "" {
			return out, errors.New("object: R2 needs an endpoint, https://<account>.r2.cloudflarestorage.com")
		}
		out.pathStyle = true
	case "b2":
		if out.region == "" {
			return out, errors.New("object: Backblaze B2 needs the bucket's region, like us-west-004")
		}
		if out.endpoint == "" {
			out.endpoint = fmt.Sprintf("https://s3.%s.backblazeb2.com", out.region)
		}
		out.pathStyle = true
	case "oss":
		if out.region == "" {
			return out, errors.New("object: Alibaba OSS needs a region, like cn-hangzhou")
		}
		if out.endpoint == "" {
			out.endpoint = fmt.Sprintf("https://s3.oss-%s.aliyuncs.com", out.region)
		}
		// OSS rejects path-style requests outright.
		out.pathStyle = false
	case "gcs":
		// GCS's S3-compatible XML API is global; the region is part of the
		// signature, and "auto" is what its interoperability layer accepts.
		if out.region == "" {
			out.region = "auto"
		}
		if out.endpoint == "" {
			out.endpoint = "https://storage.googleapis.com"
		}
		out.pathStyle = false
	default:
		return out, fmt.Errorf("object: unknown provider %q (want %s)", provider, strings.Join(Providers, ", "))
	}
	return out, nil
}

// Endpoint reports the URL a profile would talk to, for the check command and the
// form's help text.
func Endpoint(opts ClientOptions) string {
	resolved, err := resolve(opts)
	if err != nil {
		return ""
	}
	return resolved.endpoint
}

// ClientOptions is what a cloud profile turns into before it becomes a client.
type ClientOptions struct {
	// Provider is one of Providers. Each implies an endpoint shape, a region rule
	// and an addressing style; see resolve.
	Provider string
	Region   string
	Endpoint string
	// PathStyle forces path-style addressing. R2 and MinIO are happier with it;
	// AWS prefers virtual-hosted.
	PathStyle bool
	// AccessKeyID and SecretAccessKey are set when the person stored credentials
	// in the keyring. When empty, the standard AWS chain is used unchanged.
	AccessKeyID     string
	SecretAccessKey string
}

// NewClient builds an S3 client from the standard AWS credential chain: the
// environment, ~/.aws/credentials and ~/.aws/config, SSO, and instance roles.
// Nothing here reads or writes a secret; the chain is the whole point.
func NewClient(ctx context.Context, opts ClientOptions) (*s3.Client, error) {
	settings, err := resolve(opts)
	if err != nil {
		return nil, err
	}
	region := settings.region

	loadOptions := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if opts.AccessKeyID != "" && opts.SecretAccessKey != "" {
		loadOptions = append(loadOptions, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(opts.AccessKeyID, opts.SecretAccessKey, ""),
		))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("object: loading credentials: %w", err)
	}
	endpoint := settings.endpoint
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = &endpoint
		}
		o.UsePathStyle = settings.pathStyle
	}), nil
}

// Check lists a bucket's root and reports what the credentials can do. It is the
// one command that separates "wrong bucket", "wrong region" and "this key cannot
// see it" — three failures that otherwise look identical.
func Check(ctx context.Context, api API, bucket, prefix string) (files, folders int, err error) {
	fsys, err := New(Options{API: api, Bucket: bucket, Prefix: prefix, Label: bucket})
	if err != nil {
		return 0, 0, err
	}
	entries, err := fsys.ReadDir("/")
	if err != nil {
		return 0, 0, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			folders++
			continue
		}
		files++
	}
	return files, folders, nil
}
