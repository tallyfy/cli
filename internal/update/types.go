// Package update implements self-update from GitHub Releases (spec §6.11):
// channel-aware version checks, sha256 verification against checksums.txt,
// atomic binary replacement (with the Windows .old rename dance), a Homebrew
// path guard, and a passive once-per-24h update notice that never blocks.
//
// THIS FILE IS THE FROZEN CONTRACT consumed by internal/cli.
package update

// Repo coordinates for release lookups.
const (
	RepoOwner = "tallyfy"
	RepoName  = "cli"
)

// EnvNoUpdateCheck disables the passive update notice when set (non-empty,
// not "0"). Explicit `tallyfy update` still works.
const EnvNoUpdateCheck = "TALLYFY_NO_UPDATE_CHECK"

// Channel selects which releases are eligible.
type Channel string

// Channel values select which releases are eligible for updates.
const (
	ChannelStable Channel = "stable" // GET releases/latest (no prereleases)
	ChannelLatest Channel = "latest" // newest by semver including prereleases
)

// CheckResult is the outcome of a version check.
type CheckResult struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Channel         string `json:"channel"`
	AssetURL        string `json:"-"`
	ChecksumsURL    string `json:"-"`
	AssetName       string `json:"-"`
}
