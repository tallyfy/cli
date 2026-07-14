package auth

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// App settings pages where users copy their API token (`tallyfy login`
// paste flow).
const (
	productionLoginURL = "https://go.tallyfy.com/#/settings/integrations"
	stagingLoginURL    = "https://staging.go.tallyfy.com/#/settings/integrations"
)

// LoginURL maps an API base URL to the web-app settings page that shows the
// user's API token:
//
//	api.tallyfy.com (and any unrecognized host)  -> production app
//	staging.go.tallyfy.com/api, staging-api.*    -> staging app
func LoginURL(baseURL string) string {
	host := hostOf(baseURL)
	switch {
	case host == "":
		return productionLoginURL
	case host == "staging.go.tallyfy.com",
		strings.Contains(host, "staging-api"),
		strings.HasPrefix(host, "staging."):
		return stagingLoginURL
	default:
		return productionLoginURL
	}
}

// hostOf extracts a lowercase hostname, tolerating scheme-less input like
// "api.tallyfy.com". Returns "" when nothing host-like can be parsed.
func hostOf(baseURL string) string {
	s := strings.TrimSpace(baseURL)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		u, err = url.Parse("https://" + s)
		if err != nil || u.Host == "" {
			return ""
		}
	}
	return strings.ToLower(u.Hostname())
}

// OpenBrowser opens url in the platform's default browser. Best-effort:
// callers treat an error as non-fatal and print the URL for manual opening.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux and other unix: freedesktop opener
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opening browser: %w", err)
	}
	go func() { _ = cmd.Wait() }() // reap; outcome is best-effort
	return nil
}
