package config

import "os"

// WriteExampleFile writes the commented example configuration to path.
func WriteExampleFile(path string) error {
	return os.WriteFile(path, []byte(exampleConfig), 0o600)
}

// exampleConfig is what `omssh config init` writes: every option, commented,
// and valid as-is.
const exampleConfig = `# omssh configuration
# Location: $XDG_CONFIG_HOME/omssh/config.yaml (default ~/.config/omssh/)
# Connections live in ~/.ssh/config; only deploy profiles live here.

version: 1

ui:
  # auto | amber | midnight | dawn | mist | mono | contrast
  # amber is the default; auto follows the terminal (midnight or dawn)
  theme: amber
  # unicode | ascii | nerd
  # unicode works in any font; nerd needs a patched font (else you see "?")
  icons: unicode
  # full | reduced (swaps animation for static text)
  motion: full
  sidebar: true
  preview: true
  show_hidden: true      # dotfiles are visible; "." toggles them anywhere
  # home | files — the launcher, or straight into the file manager
  start: home
  # Bulk operations above these thresholds ask before starting.
  confirm_threshold:
    files: 20
    bytes: 10485760
  onboarding_done: false
  # Columns to keep free at the right edge. Some terminals draw a few glyphs one
  # cell wider than the interface counts them, and the wrapped line scrolls the
  # whole frame. Set 1 or 2 if the layout ever shifts while you type.
  width_margin: 0
  # auto: paint the theme's background only when the theme and the terminal
  # disagree (a light theme on a dark terminal). always | never.
  paint_background: auto

connections:
  # Hosts pinned to the top of the list, in this order.
  favorites: []
  # Hosts left out of the list. Code hosts (github.com, bitbucket.org) are hidden
  # automatically; H shows everything.
  hidden: []
  # Filled in as you connect, newest first.
  recent: []

cloud:
  # Buckets are places like hosts: Enter opens one in a pane, with the same copy,
  # archive and deploy behaviour.
  #
  # Credentials come from the standard AWS chain: AWS_ACCESS_KEY_ID and
  # AWS_SECRET_ACCESS_KEY, ~/.aws/credentials, SSO, or an instance role. Nothing
  # secret belongs in this file.
  profiles: {}
  # Example:
  #
  # profiles:
  #   backups:                          # AWS S3
  #     provider: s3
  #     bucket: my-backups
  #     region: eu-central-1
  #     prefix: omssh                   # optional: root the pane inside the bucket
  #
  #   r2-assets:                        # Cloudflare R2
  #     provider: r2
  #     bucket: assets
  #     endpoint: https://<account>.r2.cloudflarestorage.com
  #     region: auto
  #
  #   b2-archive:                       # Backblaze B2 (S3 API)
  #     provider: b2
  #     bucket: my-archive
  #     region: us-west-004             # the bucket's region; the endpoint follows
  #
  #   gcs-hmac:                         # Google Cloud Storage, HMAC keys (S3 XML API)
  #     provider: gcs
  #     bucket: my-backups
  #     auth: s3                        # the default for gcs
  #     region: auto
  #
  #   gcs-backups:                      # Google Cloud Storage, native client
  #     provider: gcs
  #     bucket: my-backups
  #     auth: adc                       # adc · service_account · impersonate
  #     # auth: service_account takes a credentials_file (a JSON key path)
  #     # auth: impersonate needs service_account: deployer@project.iam.gserviceaccount.com
  #
  #   oss-cn:                           # Alibaba Cloud OSS (S3-compatible)
  #     provider: oss
  #     bucket: my-bucket
  #     region: cn-hangzhou             # virtual-hosted only; the endpoint follows
  #
  #   blob-site:                        # Azure Blob Storage
  #     provider: azure
  #     account: mystorageaccount       # the account name, not a secret
  #     bucket: site                    # the container becomes "/"
  #     auth: entra                     # key · sas · entra (Entra ID by default)
  #     prefix: public                  # optional: root the pane inside it
  #     endpoint: https://mystorageaccount.blob.core.windows.net/
  #     # key and sas take their secret from the keyring, never from here
  #
  #   nextcloud:                        # WebDAV
  #     provider: webdav
  #     endpoint: https://cloud.example.com/remote.php/dav/files/me/
  #     username: me                    # password goes in the keyring

# SSH connections kept by omssh instead of ~/.ssh/config, usually because they use
# a password. The password goes in the OS keyring; this file holds the address.
hosts: {}
  # pw-db:
  #   host: 203.0.113.10
  #   user: deploy
  #   port: 22

# Saved forwards. The tunnel form saves one with the s key; History can start, edit or
# delete them.
tunnels:
  profiles: {}
  # dev-db:
  #   host: contabo
  #   kind: local              # local | remote | dynamic
  #   listen: 127.0.0.1:8080
  #   target: db.internal:5432

keys:
  # Ctrl+Q quits. "esc" is rejected: Esc only ever dismisses or goes back.
  quit: ctrl+q
  # Rebind individual actions by id, e.g.:
  # overrides:
  #   file.delete: "ctrl+d"

sessions:
  # Record embedded terminals (U) to asciinema .cast files under the state
  # directory, mode 0600. They appear in History. Set false to record nothing;
  # nothing is recorded from the external terminal, T, because omssh never sees
  # those bytes.
  record: true
  # Keystrokes go into the recording too, so a replay shows the whole exchange.
  record_input: true
  # Delete recordings older than this; 0 keeps them all. max_mb caps the folder,
  # oldest first. 0 means 512 MB.
  keep_days: 0
  max_mb: 512

transfers:
  # Files copied in parallel.
  workers: 4

deploy:
  profiles: {}
  # Example:
  #
  # profiles:
  #   prod-api:
  #     host: prod-api
  #     local_root: ./dist
  #     remote_root: /srv/api/current
  #     mode: sync                     # sync (delete extraneous) | update
  #     exclude:
  #       - .git/
  #       - "*.log"
  #       - node_modules/
  #       - .env
  #     include_hidden: false
`
