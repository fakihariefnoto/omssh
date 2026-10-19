// Command omssh is a modern SSH/SFTP client for the terminal.
package main

import (
	"os"

	"github.com/fakihariefnoto/omssh/internal/cli"
	internal "github.com/fakihariefnoto/omssh/internal/version"
)

// Injected at link time by the Makefile and goreleaser (-X main.version=...).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	internal.Version = version
	internal.Commit = commit
	internal.Date = date
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
