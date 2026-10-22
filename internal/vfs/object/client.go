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

// ClientOptions is what a cloud profile turns into before it becomes a client.
type ClientOptions struct {
	// Provider is "s3" or "r2". R2 needs an endpoint and uses "auto" as its
	// region; S3 derives both from the region.
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
	region := strings.TrimSpace(opts.Region)
	if region == "" {
		switch opts.Provider {
		case "r2":
			region = "auto"
		case "s3":
			return nil, errors.New("object: a region is required for AWS S3")
		default:
			return nil, fmt.Errorf("object: unknown provider %q (want s3 or r2)", opts.Provider)
		}
	}
	if opts.Provider == "r2" && strings.TrimSpace(opts.Endpoint) == "" {
		return nil, errors.New("object: R2 needs an endpoint, https://<account>.r2.cloudflarestorage.com")
	}

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
	endpoint := strings.TrimSpace(opts.Endpoint)
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = &endpoint
		}
		o.UsePathStyle = opts.PathStyle || opts.Provider == "r2"
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
