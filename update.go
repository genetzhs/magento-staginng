package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// githubRepo is the source repository for update checks.
const githubRepo = "genetzhs/magento-staginng"

// updateCheckInterval is the minimum time between automatic update checks.
const updateCheckInterval = 24 * time.Hour

// updateCacheFile is the basename of the on-disk cache file.
const updateCacheFile = ".magento-staging-update-check.json"

// updateCache is persisted to disk so we don't hit the GitHub API on every
// invocation.
type updateCache struct {
	LastCheck time.Time `json:"last_check"`
	LatestTag string    `json:"latest_tag"`
	LatestURL string    `json:"latest_url,omitempty"`
}

// maybeCheckUpdate performs a non-blocking update check at most once per
// updateCheckInterval. The result is printed to stderr as a one-line notice;
// the calling command is never interrupted or failed. It is safe to call
// from main() before any subcommand runs.
func maybeCheckUpdate() {
	// Skip for untagged local builds — there's no version to compare against.
	cv := strings.TrimPrefix(version, "v")
	if cv == "" || cv == "dev" || cv == "none" {
		return
	}

	cachePath, _ := updateCachePath()

	// Try to use the cached result if it's still fresh.
	if cachePath != "" {
		if cache, err := loadUpdateCache(cachePath); err == nil {
			if time.Since(cache.LastCheck) < updateCheckInterval {
				if cache.LatestTag != "" && isUpdateNewer(version, cache.LatestTag) {
					printUpdateNotice(cache.LatestTag, cache.LatestURL)
				}
				return
			}
		}
	}

	// Fresh check (best-effort; never fails the program).
	tag, htmlURL, err := fetchLatestRelease()
	if err != nil {
		// Record the attempt so we don't hammer GitHub when offline.
		if cachePath != "" {
			_ = saveUpdateCache(cachePath, updateCache{LastCheck: time.Now()})
		}
		return
	}

	if cachePath != "" {
		_ = saveUpdateCache(cachePath, updateCache{
			LastCheck: time.Now(),
			LatestTag: tag,
			LatestURL: htmlURL,
		})
	}

	if isUpdateNewer(version, tag) {
		printUpdateNotice(tag, htmlURL)
	}
}

// checkGitHubUpdate is the explicit `check-update` subcommand. It always
// performs a fresh API request and prints a status line, then exits.
func checkGitHubUpdate() {
	infof("checking for updates...")

	tag, htmlURL, err := fetchLatestRelease()
	if err != nil {
		warnf("update check failed: %v", err)
		os.Exit(1)
	}

	// Refresh the cache so the next `maybeCheckUpdate()` is fast.
	if cachePath, err := updateCachePath(); err == nil {
		_ = saveUpdateCache(cachePath, updateCache{
			LastCheck: time.Now(),
			LatestTag: tag,
			LatestURL: htmlURL,
		})
	}

	if isUpdateNewer(version, tag) {
		infof("a newer version is available: %s (you have %s)", tag, version)
		if htmlURL != "" {
			infof("download: %s", htmlURL)
		}
		os.Exit(0)
	}

	infof("you are running the latest version (%s)", version)
	os.Exit(0)
}

// fetchLatestRelease calls the GitHub releases API and returns
// (tag_name, html_url, error). Timeout is 10 seconds.
func fetchLatestRelease() (string, string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", "", err
	}
	if release.TagName == "" {
		return "", "", fmt.Errorf("empty tag_name in response")
	}
	return release.TagName, release.HTMLURL, nil
}

// printUpdateNotice prints a non-blocking "update available" notice to stderr.
// Uses the same color scheme as printWarn but stays on its own line so the
// following command output is not disturbed.
func printUpdateNotice(tag, url string) {
	fmt.Fprintf(os.Stderr, "\n%s%s%s A new version is available: %s%s%s (you have %s)\n",
		colorYellow, iconWarn, colorReset,
		colorBold, tag, colorReset, version)
	if url != "" {
		fmt.Fprintf(os.Stderr, "  %s→%s %s\n", colorWhite, colorReset, url)
	}
	fmt.Fprintf(os.Stderr, "  %s→%s run %scheck-update%s to verify, or download the new release.\n\n",
		colorWhite, colorReset, colorBold, colorReset)
}

// isUpdateNewer returns true if `latest` is strictly newer than `current`
// using semantic-version comparison (v1.2.3 > v1.2.2). Falls back to a
// lexicographic comparison if either version cannot be parsed.
func isUpdateNewer(current, latest string) bool {
	cv := parseSemver(strings.TrimPrefix(current, "v"))
	lv := parseSemver(strings.TrimPrefix(latest, "v"))
	if cv == nil || lv == nil {
		return latest != current && latest != ""
	}
	if lv[0] != cv[0] {
		return lv[0] > cv[0]
	}
	if lv[1] != cv[1] {
		return lv[1] > cv[1]
	}
	return lv[2] > cv[2]
}

// parseSemver parses "1.2.3" (or "1.2.3-rc1") into a [3]int. Returns nil on
// error. Missing minor/patch segments are treated as 0.
func parseSemver(s string) []int {
	// strip pre-release / build suffix
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return nil
	}
	out := []int{0, 0, 0}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// updateCachePath returns the absolute path to the update-check cache file.
// Preference: $XDG_CACHE_HOME, then $HOME/.cache, then os.TempDir().
// Returns "" if no usable directory could be found.
func updateCachePath() (string, error) {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, updateCacheFile), nil
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dir := filepath.Join(home, ".cache")
		if err := os.MkdirAll(dir, 0755); err == nil {
			return filepath.Join(dir, updateCacheFile), nil
		}
	}
	return filepath.Join(os.TempDir(), updateCacheFile), nil
}

// loadUpdateCache reads and parses the cache file.
func loadUpdateCache(path string) (updateCache, error) {
	var c updateCache
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(data, &c)
	return c, err
}

// saveUpdateCache writes the cache file atomically (best-effort).
func saveUpdateCache(path string, c updateCache) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
