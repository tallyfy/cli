// Package version holds build-time version metadata injected via -ldflags.
package version

// These variables are set at build time by GoReleaser (see .goreleaser.yaml).
var (
	// Version is the semantic version of this build, without the "v" prefix
	// for release builds (e.g. "0.1.0"). "dev" for source builds.
	Version = "dev"
	// Commit is the git commit SHA this binary was built from.
	Commit = "none"
	// Date is the RFC 3339 build timestamp.
	Date = "unknown"
)
