package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func makeTarGz(t *testing.T, member string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: member, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	// Extra file, as real archives carry LICENSE/README too.
	extra := []byte("license text")
	if err := tw.WriteHeader(&tar.Header{Name: "LICENSE", Mode: 0o644, Size: int64(len(extra)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(extra); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, member string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func fakeExecutable(t *testing.T, path string) {
	t.Helper()
	old := executablePath
	executablePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { executablePath = old })
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestIsBrewPath(t *testing.T) {
	cases := map[string]bool{
		"/usr/local/Cellar/tallyfy/0.1.0/bin/tallyfy":              true,
		"/opt/homebrew/Cellar/tallyfy/0.1.0/bin/tallyfy":           true,
		"/usr/local/Caskroom/tallyfy/0.1.0/tallyfy":                true,
		"/home/linuxbrew/.linuxbrew/bin/tallyfy":                   true,
		"/usr/local/bin/tallyfy":                                   false,
		"/Users/jo/bin/tallyfy":                                    false,
		"C:\\Program Files\\tallyfy\\tallyfy.exe":                  false,
		"/Users/jo/Cellars/tallyfy":                                false, // not the brew marker
		"/home/user/go/src/github.com/tallyfy/cli/dist/tallyfy":    false,
		"/usr/local/Homebrew/Cellar/tallyfy/0.1.0/bin/tallyfy":     true,
		"/opt/homebrew/Caskroom/tallyfy/latest/tallyfy-cli/binary": true,
	}
	for path, want := range cases {
		if got := isBrewPath(path); got != want {
			t.Errorf("isBrewPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestApplyBrewGuard(t *testing.T) {
	fakeExecutable(t, "/usr/local/Cellar/tallyfy/0.1.0/bin/tallyfy")
	var buf bytes.Buffer
	err := Apply(context.Background(), &CheckResult{AssetName: "x.tar.gz"}, &buf)
	var brewErr *BrewInstallError
	if !errors.As(err, &brewErr) {
		t.Fatalf("expected BrewInstallError, got %v", err)
	}
	if !strings.Contains(err.Error(), "brew upgrade tallyfy/tap/tallyfy") {
		t.Errorf("error should tell the user the brew command: %v", err)
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "asset.tar.gz")
	payload := []byte("archive-bytes")
	writeFile(t, archive, payload)

	sums := filepath.Join(dir, "checksums.txt")
	good := "0000deadbeef  other_file.tar.gz\n" + sha256Hex(payload) + "  asset.tar.gz\n"
	writeFile(t, sums, []byte(good))
	if err := verifyChecksum(archive, sums, "asset.tar.gz"); err != nil {
		t.Errorf("valid checksum rejected: %v", err)
	}

	writeFile(t, sums, []byte(sha256Hex([]byte("tampered"))+"  asset.tar.gz\n"))
	err := verifyChecksum(archive, sums, "asset.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "checksum verification failed") {
		t.Errorf("tampered checksum: got %v", err)
	}

	writeFile(t, sums, []byte("aabbcc  something-else.tar.gz\n"))
	err = verifyChecksum(archive, sums, "asset.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Errorf("missing entry: got %v", err)
	}
}

func TestExtractBinaryTarGz(t *testing.T) {
	dir := t.TempDir()
	content := []byte("#!/bin/sh\necho new-binary\n")
	archive := filepath.Join(dir, "a.tar.gz")
	writeFile(t, archive, makeTarGz(t, "tallyfy", content))

	got, err := extractBinary(archive, "a.tar.gz", "tallyfy")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("extracted %q, want %q", got, content)
	}

	if _, err := extractBinary(archive, "a.tar.gz", "missing-member"); err == nil {
		t.Error("expected error for missing member")
	}
}

func TestExtractBinaryZip(t *testing.T) {
	dir := t.TempDir()
	content := []byte("MZ fake windows binary")
	archive := filepath.Join(dir, "a.zip")
	writeFile(t, archive, makeZip(t, "tallyfy.exe", content))

	got, err := extractBinary(archive, "a.zip", "tallyfy.exe")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("extracted %q, want %q", got, content)
	}

	if _, err := extractBinary(archive, "a.zip", "tallyfy"); err == nil {
		t.Error("expected error for missing member")
	}
}

// applyFixture serves a real archive + checksums over httptest and returns a
// ready CheckResult targeting them.
func applyFixture(t *testing.T, newContent []byte, tamper bool) *CheckResult {
	t.Helper()
	binName := "tallyfy"
	if runtime.GOOS == "windows" {
		binName = "tallyfy.exe"
	}
	assetName := assetNameFor("v0.2.0", runtime.GOOS, runtime.GOARCH)
	var archive []byte
	if strings.HasSuffix(assetName, ".zip") {
		archive = makeZip(t, binName, newContent)
	} else {
		archive = makeTarGz(t, binName, newContent)
	}
	sum := sha256Hex(archive)
	if tamper {
		sum = sha256Hex([]byte("evil"))
	}
	checksums := sum + "  " + assetName + "\nffff  decoy.tar.gz\n"

	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) })
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(checksums)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &CheckResult{
		Current:         "0.1.0",
		Latest:          "v0.2.0",
		UpdateAvailable: true,
		Channel:         "stable",
		AssetName:       assetName,
		AssetURL:        srv.URL + "/asset",
		ChecksumsURL:    srv.URL + "/checksums.txt",
	}
}

func TestApplyEndToEnd(t *testing.T) {
	target := filepath.Join(t.TempDir(), "tallyfy")
	writeFile(t, target, []byte("old-binary"))
	fakeExecutable(t, target)

	newContent := []byte("#!/bin/sh\necho v0.2.0\n")
	res := applyFixture(t, newContent, false)

	var buf bytes.Buffer
	if err := Apply(context.Background(), res, &buf); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newContent) {
		t.Errorf("target not replaced: %q", got)
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Errorf(".old file should have been removed, stat err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Downloading", "Verifying checksum", "Installing", "Updated tallyfy to v0.2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("progress output missing %q:\n%s", want, out)
		}
	}
}

func TestApplyChecksumMismatchRefusesInstall(t *testing.T) {
	target := filepath.Join(t.TempDir(), "tallyfy")
	original := []byte("old-binary")
	writeFile(t, target, original)
	fakeExecutable(t, target)

	res := applyFixture(t, []byte("new"), true)

	var buf bytes.Buffer
	err := Apply(context.Background(), res, &buf)
	if err == nil || !strings.Contains(err.Error(), "checksum verification failed") {
		t.Fatalf("expected checksum failure, got %v", err)
	}
	got, _ := os.ReadFile(target)
	if !bytes.Equal(got, original) {
		t.Errorf("target must be untouched on checksum failure, got %q", got)
	}
}

func TestApplyNilResult(t *testing.T) {
	if err := Apply(context.Background(), nil, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error for nil result")
	}
}

func TestCleanupOldBinary(t *testing.T) {
	target := filepath.Join(t.TempDir(), "tallyfy")
	writeFile(t, target, []byte("bin"))
	writeFile(t, target+".old", []byte("old"))
	fakeExecutable(t, target)

	CleanupOldBinary()
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Errorf(".old not removed, stat err = %v", err)
	}
	// Idempotent when nothing to clean.
	CleanupOldBinary()
}
