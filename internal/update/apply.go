package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/minio/selfupdate"
)

// BrewInstallError signals that the running binary is Homebrew-managed and
// must be updated through brew instead of self-update.
type BrewInstallError struct {
	Path string
}

func (e *BrewInstallError) Error() string {
	return fmt.Sprintf("this tallyfy binary is managed by Homebrew (%s); update it with: brew upgrade tallyfy/tap/tallyfy", e.Path)
}

// executablePath resolves the real (symlink-free) path of the running
// binary; package variable so tests can substitute a temp target.
var executablePath = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// isBrewPath reports whether a resolved binary path lives inside a Homebrew
// prefix (formula Cellar, cask Caskroom, or Homebrew-on-Linux).
func isBrewPath(path string) bool {
	p := filepath.ToSlash(path)
	for _, marker := range []string{"/Cellar/", "/Caskroom/", "/linuxbrew/"} {
		if strings.Contains(p, marker) {
			return true
		}
	}
	return false
}

// Apply downloads the release asset from res, verifies its sha256 against
// checksums.txt, extracts the tallyfy binary, and atomically replaces the
// running executable (keeping <target>.old for rollback until cleanup).
// Progress lines are written to w.
func Apply(ctx context.Context, res *CheckResult, w io.Writer) error {
	if res == nil {
		return errors.New("update: nil check result")
	}
	target, err := executablePath()
	if err != nil {
		return fmt.Errorf("cannot locate running binary: %w", err)
	}
	if isBrewPath(target) {
		return &BrewInstallError{Path: target}
	}

	tmpDir, err := os.MkdirTemp("", "tallyfy-update-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	_, _ = fmt.Fprintf(w, "Downloading %s...\n", res.AssetName)
	archivePath := filepath.Join(tmpDir, res.AssetName)
	if err := downloadFile(ctx, res.AssetURL, archivePath); err != nil {
		return fmt.Errorf("download %s: %w", res.AssetName, err)
	}
	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	if err := downloadFile(ctx, res.ChecksumsURL, checksumsPath); err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}

	_, _ = fmt.Fprintln(w, "Verifying checksum...")
	if err := verifyChecksum(archivePath, checksumsPath, res.AssetName); err != nil {
		return err
	}

	binName := "tallyfy"
	if runtime.GOOS == "windows" {
		binName = "tallyfy.exe"
	}
	_, _ = fmt.Fprintf(w, "Extracting %s...\n", binName)
	binBytes, err := extractBinary(archivePath, res.AssetName, binName)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "Installing to %s...\n", target)
	opts := selfupdate.Options{
		TargetPath:  target,
		OldSavePath: target + ".old",
	}
	if err := selfupdate.Apply(bytes.NewReader(binBytes), opts); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}
	// Best-effort: on Windows the running old binary cannot be deleted yet;
	// CleanupOldBinary removes it on the next start.
	_ = os.Remove(target + ".old")
	_, _ = fmt.Fprintf(w, "Updated tallyfy to %s\n", res.Latest)
	return nil
}

// CleanupOldBinary removes the <executable>.old file a previous update may
// have left behind (Windows cannot delete the running binary during the
// update itself). Best-effort: all errors are ignored. Called at startup.
func CleanupOldBinary() {
	target, err := executablePath()
	if err != nil {
		return
	}
	_ = os.Remove(target + ".old")
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	f, err := os.Create(dest) //nolint:gosec // G304: dest is inside our own os.MkdirTemp dir; asset name comes from trusted release metadata
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// verifyChecksum finds the checksums.txt line whose second field equals
// assetName ("<sha256-hex>  <filename>") and compares it with the sha256 of
// the downloaded archive.
func verifyChecksum(archivePath, checksumsPath, assetName string) error {
	f, err := os.Open(checksumsPath) //nolint:gosec // G304: checksumsPath is inside our own os.MkdirTemp dir
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	want := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[1] == assetName {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", assetName)
	}

	af, err := os.Open(archivePath) //nolint:gosec // G304: archivePath is inside our own os.MkdirTemp dir
	if err != nil {
		return err
	}
	defer func() { _ = af.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, af); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return errors.New("checksum verification failed - refusing to install")
	}
	return nil
}

// extractBinary pulls the named binary member out of a .tar.gz or .zip
// archive (chosen by the assetName extension).
func extractBinary(archivePath, assetName, binName string) ([]byte, error) {
	if strings.HasSuffix(assetName, ".zip") {
		return extractZip(archivePath, binName)
	}
	return extractTarGz(archivePath, binName)
}

func extractTarGz(archivePath, binName string) ([]byte, error) {
	f, err := os.Open(archivePath) //nolint:gosec // G304: archivePath is inside our own os.MkdirTemp dir
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == binName {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("archive %s does not contain %s", filepath.Base(archivePath), binName)
}

func extractZip(archivePath, binName string) ([]byte, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || filepath.Base(zf.Name) != binName {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, fmt.Errorf("archive %s does not contain %s", filepath.Base(archivePath), binName)
}
