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
  # auto | midnight | dawn | mist | ember | mono | contrast
  theme: auto
  # nerd | ascii
  icons: nerd
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

keys:
  # standard | vim (vim changes navigation only; operations stay standard)
  preset: standard
  # Ctrl+C is copy. Set to "ctrl+insert" if you want Ctrl+C to mean quit.
  copy: ctrl+c
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
  #     follow_symlinks: false
  #     atomic: true                   # upload to .omssh-tmp then rename
  #     preserve_perms: true
  #     backup: true                   # keep one previous version
  #     workers: 4
  #     hooks:
  #       local_pre:  ["npm run build"]
  #       remote_post:
  #         - sudo systemctl restart api
`
