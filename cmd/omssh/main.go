// Command omssh is a modern SSH/SFTP client for the terminal.
package main

import (
	"os"

	"github.com/fakihariefnoto/omssh/internal/cli"
	"github.com/fakihariefnoto/omssh/internal/version"
)

// Injected at link time by the Makefile and goreleaser.
var (
	version_ = "dev"
	commit   = "none"
	date     = "unknown"
)

func main() {
	version.Version = version_
	version.Commit = commit
	version.Date = date
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
