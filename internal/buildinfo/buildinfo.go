// Package buildinfo centralizes build metadata shared by the CLI (version
// command, --version) and the web server (/api/v1/version, UI sidebar badge).
// Values are injected at build time via -ldflags -X (see Taskfile/Makefile).
package buildinfo

import "fmt"

var (
	Version = "0.7.0"
	Commit  = "unknown"
	Date    = "unknown"
)

// Short returns the bare version, suitable for a compact UI badge.
func Short() string { return Version }

// String appends commit/date when ldflags injected them; without metadata it
// stays short so dev builds don't show noise.
func String() string {
	if Commit == "unknown" && Date == "unknown" {
		return Version
	}
	return fmt.Sprintf("%s (commit: %s, built: %s)", Version, Commit, Date)
}
