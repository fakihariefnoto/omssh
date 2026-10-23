package object

import "testing"

// Each provider implies an endpoint, a region and an addressing style. These are
// the facts that cost an afternoon when they are wrong.
func TestProviderPresets(t *testing.T) {
	cases := []struct {
		name      string
		opts      ClientOptions
		endpoint  string
		region    string
		pathStyle bool
	}{
		{
			name: "aws",
			opts: ClientOptions{Provider: "s3", Region: "eu-central-1"},
			// AWS is left to the SDK: no endpoint override.
			endpoint: "", region: "eu-central-1", pathStyle: false,
		},
		{
			name:     "r2",
			opts:     ClientOptions{Provider: "r2", Endpoint: "https://acct.r2.cloudflarestorage.com"},
			endpoint: "https://acct.r2.cloudflarestorage.com", region: "auto", pathStyle: true,
		},
		{
			name:     "b2",
			opts:     ClientOptions{Provider: "b2", Region: "us-west-004"},
			endpoint: "https://s3.us-west-004.backblazeb2.com", region: "us-west-004", pathStyle: true,
		},
		{
			name:     "gcs",
			opts:     ClientOptions{Provider: "gcs"},
			endpoint: "https://storage.googleapis.com", region: "auto", pathStyle: false,
		},
		{
			name:     "oss",
			opts:     ClientOptions{Provider: "oss", Region: "cn-hangzhou"},
			endpoint: "https://s3.oss-cn-hangzhou.aliyuncs.com", region: "cn-hangzhou", pathStyle: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolve(tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got.endpoint != tc.endpoint {
				t.Errorf("endpoint = %q, want %q", got.endpoint, tc.endpoint)
			}
			if got.region != tc.region {
				t.Errorf("region = %q, want %q", got.region, tc.region)
			}
			if got.pathStyle != tc.pathStyle {
				t.Errorf("pathStyle = %v, want %v", got.pathStyle, tc.pathStyle)
			}
		})
	}
}

// The three ways to get a provider wrong are refused with the missing thing named.
func TestProviderPresetsRefuseIncompleteProfiles(t *testing.T) {
	for _, opts := range []ClientOptions{
		{Provider: "s3"},   // no region
		{Provider: "r2"},   // no endpoint
		{Provider: "b2"},   // no region
		{Provider: "oss"},  // no region
		{Provider: "nope"}, // unknown
	} {
		if _, err := resolve(opts); err == nil {
			t.Errorf("%+v should be refused", opts)
		}
	}
}

// An explicit endpoint always wins: that is how a private MinIO or a proxy is
// addressed with the same client.
func TestExplicitEndpointWins(t *testing.T) {
	got, err := resolve(ClientOptions{Provider: "b2", Region: "us-west-004", Endpoint: "https://proxy.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got.endpoint != "https://proxy.example.com" {
		t.Errorf("endpoint = %q", got.endpoint)
	}
}
