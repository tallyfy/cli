package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// APIBaseURL is the GitHub API base URL; package variable so tests can point
// Check at an httptest server.
var APIBaseURL = "https://api.github.com"

// httpClient performs release-metadata and asset requests; overridable in
// tests. Timeouts are applied per request via context.
var httpClient = &http.Client{}

// checkTimeout bounds each release-metadata request.
const checkTimeout = 5 * time.Second

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type release struct {
	TagName    string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

// Check queries GitHub Releases for the newest version on a channel and
// resolves the download/checksum assets for this platform.
//
// ChannelStable uses releases/latest (GitHub already excludes drafts and
// prereleases); ChannelLatest scans the newest 20 releases and picks the
// highest semver tag including prereleases, skipping drafts. When current is
// "dev" (source build) or not valid semver, UpdateAvailable is always false
// but Latest is still filled.
func Check(ctx context.Context, current string, ch Channel) (*CheckResult, error) {
	if ch == "" {
		ch = ChannelStable
	}
	var rel *release
	var err error
	switch ch {
	case ChannelStable:
		rel, err = fetchStable(ctx)
	case ChannelLatest:
		rel, err = fetchLatestIncludingPrereleases(ctx)
	default:
		return nil, fmt.Errorf("unknown update channel %q (valid: %s, %s)", string(ch), ChannelStable, ChannelLatest)
	}
	if err != nil {
		return nil, err
	}

	latest := normalizeV(rel.TagName)
	res := &CheckResult{
		Current: current,
		Latest:  latest,
		Channel: string(ch),
	}
	if cur := normalizeV(current); current != "dev" && semver.IsValid(cur) && semver.IsValid(latest) {
		res.UpdateAvailable = semver.Compare(latest, cur) > 0
	}

	res.AssetName = assetNameFor(latest, runtime.GOOS, runtime.GOARCH)
	for _, a := range rel.Assets {
		switch a.Name {
		case res.AssetName:
			res.AssetURL = a.BrowserDownloadURL
		case "checksums.txt":
			res.ChecksumsURL = a.BrowserDownloadURL
		}
	}
	if res.AssetURL == "" {
		return nil, fmt.Errorf("release %s has no asset %q for %s/%s", rel.TagName, res.AssetName, runtime.GOOS, runtime.GOARCH)
	}
	if res.ChecksumsURL == "" {
		return nil, fmt.Errorf("release %s has no checksums.txt asset", rel.TagName)
	}
	return res, nil
}

func fetchStable(ctx context.Context) (*release, error) {
	var rel release
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", APIBaseURL, RepoOwner, RepoName)
	if err := getJSON(ctx, url, &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func fetchLatestIncludingPrereleases(ctx context.Context) (*release, error) {
	var rels []release
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=20", APIBaseURL, RepoOwner, RepoName)
	if err := getJSON(ctx, url, &rels); err != nil {
		return nil, err
	}
	var best *release
	for i := range rels {
		r := &rels[i]
		if r.Draft {
			continue
		}
		tag := normalizeV(r.TagName)
		if !semver.IsValid(tag) {
			continue
		}
		if best == nil || semver.Compare(tag, normalizeV(best.TagName)) > 0 {
			best = r
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no releases found for %s/%s", RepoOwner, RepoName)
	}
	return best, nil
}

func getJSON(ctx context.Context, url string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GitHub API %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// normalizeV returns the version with exactly one leading "v".
func normalizeV(s string) string {
	return "v" + strings.TrimPrefix(s, "v")
}

// assetNameFor builds the GoReleaser archive name for a platform, e.g.
// tallyfy_0.2.0_darwin_arm64.tar.gz (zip on windows). Must stay in sync with
// archives.name_template in .goreleaser.yaml.
func assetNameFor(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("tallyfy_%s_%s_%s.%s", strings.TrimPrefix(version, "v"), goos, goarch, ext)
}
