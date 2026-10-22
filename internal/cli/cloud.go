package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/omssh/internal/cloudcred"
	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/keys"
	"github.com/fakihariefnoto/omssh/internal/vfs"
	"github.com/fakihariefnoto/omssh/internal/vfs/object"
)

// newCloudCmd inspects object-storage profiles without starting the interface.
// It exists because the three ways a bucket refuses to work — wrong name, wrong
// region, key not allowed — look identical from the outside.
func newCloudCmd(opts *Options, stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloud",
		Short: "Object storage profiles (AWS S3, Cloudflare R2)",
		Long: "Inspect the cloud.profiles in your config.\n\n" +
			"Credentials come from the standard AWS chain: AWS_ACCESS_KEY_ID and\n" +
			"AWS_SECRET_ACCESS_KEY, ~/.aws/credentials, SSO, or an instance role.",
	}
	cmd.AddCommand(newCloudListCmd(opts, stdout))
	cmd.AddCommand(newCloudCheckCmd(opts, stdout))
	cmd.AddCommand(newCloudLsCmd(opts, stdout))
	cmd.AddCommand(newCloudForgetCmd(opts, stdout))
	return cmd
}

func newCloudListCmd(opts *Options, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the configured profiles",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, _, err := loadConfig(opts)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(cfg.Cloud.Profiles))
			for name := range cfg.Cloud.Profiles {
				names = append(names, name)
			}
			sort.Strings(names)
			if len(names) == 0 {
				fmt.Fprintln(stdout, "no cloud profiles · add one under cloud.profiles in the config")
				return nil
			}
			for _, name := range names {
				profile := cfg.Cloud.Profiles[name]
				fmt.Fprintf(stdout, "%-16s %-4s s3://%s/%s\n", name, profile.Provider,
					profile.Bucket, strings.Trim(profile.Prefix, "/"))
			}
			return nil
		},
	}
}

func newCloudCheckCmd(opts *Options, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "check <profile>",
		Short: "List a bucket's root and report what the credentials can do",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			fsys, profile, err := openCloud(opts, args[0])
			if err != nil {
				return err
			}
			entries, err := fsys.ReadDir("/")
			if err != nil {
				return explainCloud(err, profile)
			}
			files, folders := 0, 0
			for _, entry := range entries {
				if entry.IsDir() {
					folders++
					continue
				}
				files++
			}
			caps := fsys.Capabilities()
			fmt.Fprintf(stdout, "%s · %s\n", fsys.String(), profile.Provider)
			fmt.Fprintf(stdout, "  %d %s · %d %s\n", folders, plural(folders, "folder", "folders"),
				files, plural(files, "file", "files"))
			fmt.Fprintf(stdout, "  rename: server-side copy · permissions: no · links: no\n")
			_ = caps
			return nil
		},
	}
}

func newCloudForgetCmd(_ *Options, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "forget <profile>",
		Short: "Remove the stored access key for a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := cloudcred.Forget(keys.Default("omssh"), args[0]); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "forgot the stored credentials for "+args[0])
			return nil
		},
	}
}

func newCloudLsCmd(opts *Options, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "ls <profile> [path]",
		Short: "List a folder in a bucket",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			fsys, profile, err := openCloud(opts, args[0])
			if err != nil {
				return err
			}
			path := "/"
			if len(args) > 1 {
				path = args[1]
			}
			entries, err := fsys.ReadDir(path)
			if err != nil {
				return explainCloud(err, profile)
			}
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() {
					name += "/"
				}
				fmt.Fprintln(stdout, name)
			}
			return nil
		},
	}
}

// openCloud turns a profile name into a filesystem.
func openCloud(opts *Options, name string) (*object.FS, config.CloudProfile, error) {
	cfg, _, err := loadConfig(opts)
	if err != nil {
		return nil, config.CloudProfile{}, err
	}
	profile, ok := cfg.Cloud.Profiles[name]
	if !ok {
		return nil, config.CloudProfile{}, fmt.Errorf("no cloud profile named %q · omssh cloud list", name)
	}
	store := keys.Default("omssh")
	accessKey, secretKey, _ := cloudcred.Load(store, name)
	client, err := object.NewClient(context.Background(), object.ClientOptions{
		Provider:        profile.Provider,
		Region:          profile.Region,
		Endpoint:        profile.Endpoint,
		PathStyle:       profile.PathStyle,
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
	})
	if err != nil {
		return nil, profile, err
	}
	fsys, err := object.New(object.Options{
		API: client, Bucket: profile.Bucket, Prefix: profile.Prefix, Label: name,
	})
	if err != nil {
		return nil, profile, err
	}
	return fsys, profile, nil
}

// explainCloud says which of the three identical-looking failures this is.
func explainCloud(err error, profile config.CloudProfile) error {
	switch {
	case errors.Is(err, vfs.ErrPermission):
		return fmt.Errorf("s3://%s: the credentials work but this key cannot see the bucket\n"+
			"  check the key's permissions, and for Backblaze B2 that the key is scoped to %q",
			profile.Bucket, profile.Bucket)
	case errors.Is(err, vfs.ErrNotExist):
		return fmt.Errorf("s3://%s: no such bucket or prefix from here\n"+
			"  check the bucket name and that the region matches where it lives (%q)",
			profile.Bucket, profile.Region)
	default:
		return err
	}
}
