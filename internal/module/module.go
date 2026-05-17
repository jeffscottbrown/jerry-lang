// Package module handles fetching, caching, and verifying remote jerry modules.
//
// Modules are identified by import paths of the form "github.com/owner/repo".
// Versions correspond to git tags (e.g. "v1.0.0"). The local cache lives at
// ~/.jerry/cache/remotes/<modpath>@<version>/ and contains only the .jer files and
// jerry.mod extracted from the upstream repository archive.
package module

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// CacheDir returns the root of the module cache (~/.jerry/cache/remotes).
func CacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".jerry", "cache", "remotes"), nil
}

// CachedDir returns the directory where a specific module version is (or will
// be) cached. It does not check whether the directory exists.
func CachedDir(modPath, version string) (string, error) {
	base, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, modPath+"@"+version), nil
}

// IsCached reports whether a module version is already in the local cache.
func IsCached(modPath, version string) (bool, error) {
	dir, err := CachedDir(modPath, version)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(dir)
	return err == nil, nil
}

// Fetch downloads a module archive, extracts .jer files to the cache, and
// returns (cacheDir, "h1:<hex>", nil). If the module is already cached the
// download is skipped and the stored hash is returned.
func Fetch(modPath, version string) (cacheDir, hash string, err error) {
	cacheDir, err = CachedDir(modPath, version)
	if err != nil {
		return "", "", err
	}
	hashFile := cacheDir + ".hash"

	// Already cached — return stored hash.
	if _, statErr := os.Stat(cacheDir); statErr == nil {
		if h, readErr := os.ReadFile(hashFile); readErr == nil {
			return cacheDir, strings.TrimSpace(string(h)), nil
		}
	}

	url := downloadURL(modPath, version)
	fmt.Fprintf(os.Stderr, "jerry: downloading %s@%s\n", modPath, version)
	body, err := httpGet(url)
	if err != nil {
		return "", "", fmt.Errorf("fetch %s@%s: %w", modPath, version, err)
	}

	// Compute hash over raw archive bytes.
	sum := sha256.Sum256(body)
	hash = "h1:" + hex.EncodeToString(sum[:])

	// Extract to cache directory.
	if mkErr := os.MkdirAll(cacheDir, 0755); mkErr != nil {
		return "", "", mkErr
	}
	if extErr := extractTarGz(body, cacheDir); extErr != nil {
		os.RemoveAll(cacheDir)
		return "", "", fmt.Errorf("extract %s@%s: %w", modPath, version, extErr)
	}

	// Persist hash alongside the cache dir.
	_ = os.WriteFile(hashFile, []byte(hash), 0644)

	return cacheDir, hash, nil
}

// VerifyHash checks that the locally stored hash for a cached module matches
// expectedHash. Returns an error if the module is not cached or hashes differ.
func VerifyHash(modPath, version, expectedHash string) error {
	cacheDir, err := CachedDir(modPath, version)
	if err != nil {
		return err
	}
	hashFile := cacheDir + ".hash"
	stored, err := os.ReadFile(hashFile)
	if err != nil {
		return fmt.Errorf("%s@%s is not cached — run: jerry get %s@%s",
			modPath, version, modPath, version)
	}
	if strings.TrimSpace(string(stored)) != expectedHash {
		return fmt.Errorf("hash mismatch for %s@%s: expected %s, have %s",
			modPath, version, expectedHash, strings.TrimSpace(string(stored)))
	}
	return nil
}

// JerFiles returns the paths of all .jer files in a cached module directory.
func JerFiles(cacheDir string) ([]string, error) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jer") {
			paths = append(paths, filepath.Join(cacheDir, e.Name()))
		}
	}
	return paths, nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func downloadURL(modPath, version string) string {
	if strings.HasPrefix(modPath, "github.com/") {
		return "https://" + modPath + "/archive/refs/tags/" + version + ".tar.gz"
	}
	// Generic fallback; works for any host that serves GitHub-compatible archives.
	return "https://" + modPath + "/archive/" + version + ".tar.gz"
}

func httpGet(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec -- URL is validated by caller
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// extractTarGz extracts .jer files and jerry.mod from a gzipped tar archive
// into destDir, stripping the top-level directory that GitHub adds to archives
// (e.g. "repo-1.0.0/"). Path traversal entries are silently skipped.
func extractTarGz(data []byte, destDir string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		// Strip the GitHub-added top-level directory component.
		parts := strings.SplitN(hdr.Name, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		relPath := parts[1]

		// Only keep .jer files and jerry.mod; skip everything else.
		if !strings.HasSuffix(relPath, ".jer") && relPath != "jerry.mod" {
			continue
		}
		// Reject path traversal attempts.
		if strings.Contains(relPath, "..") {
			continue
		}

		destPath := filepath.Join(destDir, relPath)
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}
		f, err := os.Create(destPath)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(f, tr) //nolint:gosec -- size bounded by HTTP response
		f.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
