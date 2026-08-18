package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// githubRepo is the source repository for update checks.
const githubRepo = "genetzhs/magento-staginng"

// checkGitHubUpdate queries the GitHub API for the latest release and warns
// the user if a newer version is available.
func checkGitHubUpdate() {
	infof("checking for updates...")

	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		warnf("update check failed: %v", err)
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		warnf("update check failed (network): %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		warnf("update check failed: GitHub API returned %d", resp.StatusCode)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		warnf("update check failed: %v", err)
		return
	}

	var release struct {
		TagName string `json:"tag_name"`
		Name    string `json:"name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		warnf("update check failed: %v", err)
		return
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(version, "v")

	if latest == "" {
		warnf("could not determine latest version")
		return
	}

	if latest == current {
		infof("you are running the latest version (%s)", version)
		return
	}

	infof("a newer version is available: %s (you have %s)", release.TagName, version)
	infof("download: %s", release.HTMLURL)
}

// showVersion prints the binary version.
//
// (already declared in help.go via showVersion; here we just expose a
// standalone entrypoint.)
func showVersionStandalone() {
	fmt.Printf("magento-staging %s (commit %s)\n", version, commit)
	os.Exit(0)
}
