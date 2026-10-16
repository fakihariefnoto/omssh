// Package version carries build metadata injected at link time.
package version

import (
	"fmt"
	"runtime"
)

// Set via -ldflags by the Makefile.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a one-line human readable version.
func String() string {
	return fmt.Sprintf("omssh %s (%s) built %s %s/%s",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH)
}

// Short returns just the version, for embedding in UI chrome.
func Short() string {
	return Version
}
