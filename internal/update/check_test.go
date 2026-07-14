package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// stubRelease builds a release JSON object with platform + checksums assets.
func stubRelease(tag string, draft, prerelease bool, withAssets bool) map[string]any {
	rel := map[string]any{
		"tag_name":   tag,
		"draft":      draft,
		"prerelease": prerelease,
		"assets":     []any{},
	}
	if withAssets {
		asset := assetNameFor(tag, runtime.GOOS, runtime.GOARCH)
		rel["assets"] = []any{
			map[string]any{"name": asset, "browser_download_url": "https://dl.test/" + tag + "/" + asset},
			map[string]any{"name": "checksums.txt", "browser_download_url": "https://dl.test/" + tag + "/checksums.txt"},
			map[string]any{"name": "unrelated.txt", "browser_download_url": "https://dl.test/other"},
		}
	}
	return rel
}

// githubStub serves releases/latest and the releases list, and redirects
// APIBaseURL at itself for the duration of the test.
func githubStub(t *testing.T, latest map[string]any, list []map[string]any, sawRequest func(*http.Request)) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/tallyfy/cli/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if sawRequest != nil {
			sawRequest(r)
		}
		_ = json.NewEncoder(w).Encode(latest)
	})
	mux.HandleFunc("/repos/tallyfy/cli/releases", func(w http.ResponseWriter, r *http.Request) {
		if sawRequest != nil {
			sawRequest(r)
		}
		_ = json.NewEncoder(w).Encode(list)
	})
	srv := httptest.NewServer(mux)
	old := APIBaseURL
	APIBaseURL = srv.URL
	t.Cleanup(func() {
		APIBaseURL = old
		srv.Close()
	})
}

func TestCheckStableUpdateAvailable(t *testing.T) {
	githubStub(t, stubRelease("v0.2.0", false, false, true), nil, nil)

	res, err := Check(context.Background(), "0.1.0", ChannelStable)
	if err != nil {
		t.Fatal(err)
	}
	if !res.UpdateAvailable {
		t.Error("expected update available for 0.1.0 -> v0.2.0")
	}
	if res.Latest != "v0.2.0" || res.Current != "0.1.0" || res.Channel != "stable" {
		t.Errorf("unexpected result: %+v", res)
	}
	wantAsset := assetNameFor("v0.2.0", runtime.GOOS, runtime.GOARCH)
	if res.AssetName != wantAsset {
		t.Errorf("AssetName = %q, want %q", res.AssetName, wantAsset)
	}
	if !strings.HasSuffix(res.AssetURL, wantAsset) || !strings.HasSuffix(res.ChecksumsURL, "checksums.txt") {
		t.Errorf("asset URLs not resolved: %+v", res)
	}
}

func TestCheckStableUpToDate(t *testing.T) {
	githubStub(t, stubRelease("v0.2.0", false, false, true), nil, nil)
	for _, current := range []string{"0.2.0", "v0.2.0", "0.3.0"} {
		res, err := Check(context.Background(), current, ChannelStable)
		if err != nil {
			t.Fatal(err)
		}
		if res.UpdateAvailable {
			t.Errorf("current %q: expected no update against v0.2.0", current)
		}
	}
}

func TestCheckCurrentDevNeverUpdates(t *testing.T) {
	githubStub(t, stubRelease("v9.9.9", false, false, true), nil, nil)
	res, err := Check(context.Background(), "dev", ChannelStable)
	if err != nil {
		t.Fatal(err)
	}
	if res.UpdateAvailable {
		t.Error("dev builds must never report an available update")
	}
	if res.Latest != "v9.9.9" {
		t.Errorf("Latest should still be filled for dev: %+v", res)
	}
}

func TestCheckEmptyChannelDefaultsToStable(t *testing.T) {
	githubStub(t, stubRelease("v0.2.0", false, false, true), nil, nil)
	res, err := Check(context.Background(), "0.1.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Channel != "stable" {
		t.Errorf("Channel = %q, want stable", res.Channel)
	}
}

func TestCheckLatestChannelPicksHighestSemverIncludingPrereleases(t *testing.T) {
	list := []map[string]any{
		stubRelease("v0.2.0", false, false, true),
		stubRelease("v0.3.0-rc.1", false, true, true),
		stubRelease("v9.9.9", true, false, true),         // draft: must be skipped
		stubRelease("not-a-version", false, false, true), // invalid semver: skipped
	}
	githubStub(t, nil, list, nil)

	res, err := Check(context.Background(), "0.2.0", ChannelLatest)
	if err != nil {
		t.Fatal(err)
	}
	if res.Latest != "v0.3.0-rc.1" {
		t.Errorf("Latest = %q, want v0.3.0-rc.1", res.Latest)
	}
	if !res.UpdateAvailable {
		t.Error("v0.3.0-rc.1 > v0.2.0: expected update available")
	}
	if res.Channel != "latest" {
		t.Errorf("Channel = %q, want latest", res.Channel)
	}
}

func TestCheckLatestChannelNoReleases(t *testing.T) {
	githubStub(t, nil, []map[string]any{}, nil)
	if _, err := Check(context.Background(), "0.1.0", ChannelLatest); err == nil {
		t.Fatal("expected error when no releases exist")
	}
}

func TestCheckUnknownChannel(t *testing.T) {
	if _, err := Check(context.Background(), "0.1.0", Channel("beta")); err == nil || !strings.Contains(err.Error(), "beta") {
		t.Fatalf("expected unknown-channel error, got %v", err)
	}
}

func TestCheckSendsHeaders(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "tok123")
	var gotAuth, gotAccept string
	githubStub(t, stubRelease("v0.2.0", false, false, true), nil, func(r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
	})
	if _, err := Check(context.Background(), "0.1.0", ChannelStable); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("Authorization = %q, want Bearer tok123", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
}

func TestCheckMissingPlatformAsset(t *testing.T) {
	githubStub(t, stubRelease("v0.2.0", false, false, false), nil, nil)
	_, err := Check(context.Background(), "0.1.0", ChannelStable)
	if err == nil || !strings.Contains(err.Error(), "no asset") {
		t.Fatalf("expected missing-asset error, got %v", err)
	}
}

func TestCheckMissingChecksumsAsset(t *testing.T) {
	rel := stubRelease("v0.2.0", false, false, true)
	assets := rel["assets"].([]any)
	rel["assets"] = assets[:1] // keep only the platform asset
	githubStub(t, rel, nil, nil)
	_, err := Check(context.Background(), "0.1.0", ChannelStable)
	if err == nil || !strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("expected missing-checksums error, got %v", err)
	}
}

func TestCheckHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	old := APIBaseURL
	APIBaseURL = srv.URL
	t.Cleanup(func() { APIBaseURL = old; srv.Close() })

	if _, err := Check(context.Background(), "0.1.0", ChannelStable); err == nil {
		t.Fatal("expected error on non-200 response")
	}
}

func TestAssetNameFor(t *testing.T) {
	cases := []struct {
		version, goos, goarch, want string
	}{
		{"v0.2.0", "darwin", "arm64", "tallyfy_0.2.0_darwin_arm64.tar.gz"},
		{"0.2.0", "linux", "amd64", "tallyfy_0.2.0_linux_amd64.tar.gz"},
		{"v0.2.0", "windows", "amd64", "tallyfy_0.2.0_windows_amd64.zip"},
		{"v1.0.0-rc.1", "windows", "arm64", "tallyfy_1.0.0-rc.1_windows_arm64.zip"},
	}
	for _, tc := range cases {
		if got := assetNameFor(tc.version, tc.goos, tc.goarch); got != tc.want {
			t.Errorf("assetNameFor(%q,%q,%q) = %q, want %q", tc.version, tc.goos, tc.goarch, got, tc.want)
		}
	}
}

func TestNormalizeV(t *testing.T) {
	for in, want := range map[string]string{"0.1.0": "v0.1.0", "v0.1.0": "v0.1.0"} {
		if got := normalizeV(in); got != want {
			t.Errorf("normalizeV(%q) = %q, want %q", in, got, want)
		}
	}
}
