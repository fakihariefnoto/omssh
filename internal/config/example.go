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
  show_hidden: false
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

keys:
  # Ctrl+Q quits. "esc" is rejected: Esc only ever dismisses or goes back.
  quit: ctrl+q
  # Rebind individual actions by id, e.g.:
  # overrides:
  #   file.delete: "ctrl+d"

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
