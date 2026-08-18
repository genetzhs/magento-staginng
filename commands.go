package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// runList lists all staging sites on this server by scanning the
// .credentials/ directories under each domain's webspace root.
func runList(args []string) {
	// Walk /var/www/vhosts/*/.credentials/*.json
	root := "/var/www/vhosts"
	entries, err := os.ReadDir(root)
	if err != nil {
		failf("cannot read %s: %v", root, err)
	}

	type stagingInfo struct {
		Domain      string
		StagingName string
		StagingURL  string
		CreatedAt   string
		Path        string
	}

	var stagings []stagingInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		domain := entry.Name()
		credsDir := filepath.Join(root, domain, ".credentials")
		files, err := os.ReadDir(credsDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			path := filepath.Join(credsDir, f.Name())
			creds, err := loadCredentials(path)
			if err != nil {
				continue
			}
			stagings = append(stagings, stagingInfo{
				Domain:      creds.Domain,
				StagingName: creds.StagingName,
				StagingURL:  creds.StagingURL,
				CreatedAt:   creds.CreatedAt,
				Path:        creds.TargetPath,
			})
		}
	}

	if len(stagings) == 0 {
		infof("no staging sites found on this server")
		return
	}

	sort.Slice(stagings, func(i, j int) bool {
		if stagings[i].Domain != stagings[j].Domain {
			return stagings[i].Domain < stagings[j].Domain
		}
		return stagings[i].StagingName < stagings[j].StagingName
	})

	infof("%-25s %-15s %-40s %-25s %s",
		"DOMAIN", "STAGING", "URL", "CREATED", "PATH")
	infof("%s", strings.Repeat("-", 120))
	for _, s := range stagings {
		infof("%-25s %-15s %-40s %-25s %s",
			s.Domain, s.StagingName, s.StagingURL, s.CreatedAt, s.Path)
	}
}

// runInfo shows details for a specific staging site.
func runInfo(args []string) {
	var domain, stagingName string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--domain":
			if i+1 < len(args) {
				domain = args[i+1]
				i++
			}
		case "--staging-name":
			if i+1 < len(args) {
				stagingName = args[i+1]
				i++
			}
		}
	}
	if domain == "" {
		failf("--domain is required")
	}
	if stagingName == "" {
		stagingName = "staging"
	}

	path := fmt.Sprintf("/var/www/vhosts/%s/.credentials/%s.json", domain, stagingName)
	creds, err := loadCredentials(path)
	if err != nil {
		failf("could not load credentials from %s: %v", path, err)
	}

	out, _ := json.MarshalIndent(creds, "", "  ")
	fmt.Println(string(out))
}

// runCleanup deletes a staging site (interactive confirmation by default).
func runCleanup(args []string) {
	var domain, stagingName string
	nonInteractive := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--domain":
			if i+1 < len(args) {
				domain = args[i+1]
				i++
			}
		case "--staging-name":
			if i+1 < len(args) {
				stagingName = args[i+1]
				i++
			}
		case "--non-interactive", "-y":
			nonInteractive = true
		}
	}
	if domain == "" {
		failf("--domain is required")
	}
	if stagingName == "" {
		stagingName = "staging"
	}

	// Load credentials
	credsPath := fmt.Sprintf("/var/www/vhosts/%s/.credentials/%s.json", domain, stagingName)
	creds, err := loadCredentials(credsPath)
	if err != nil {
		warnf("could not load credentials from %s: %v", credsPath, err)
		// continue anyway - we can still try to delete by convention
		creds = &credentials{
			Domain:      domain,
			StagingName: stagingName,
			StagingURL:  fmt.Sprintf("https://%s.%s/", stagingName, domain),
			TargetPath:  fmt.Sprintf("/var/www/vhosts/%s/%s", domain, stagingName),
		}
	}

	infof("About to delete:")
	infof("  Subdomain:     %s.%s", stagingName, domain)
	infof("  Path:          %s", creds.TargetPath)
	if creds.TargetDB.Name != "" {
		infof("  Database:      %s", creds.TargetDB.Name)
	}
	if creds.TargetDB.User != "" {
		infof("  DB user:       %s", creds.TargetDB.User)
	}

	if !nonInteractive {
		if !promptConfirm("\nConfirm deletion? This cannot be undone", false) {
			infof("aborted")
			os.Exit(0)
		}
	}

	// Remove subdomain (also removes files under the subdomain path)
	infof("removing subdomain...")
	if err := pleskSubdomainRemove(&config{
		domain:      domain,
		stagingName:  stagingName,
	}); err != nil {
		warnf("subdomain removal failed: %v", err)
	}

	// Remove target path (in case it's outside subdomain removal scope)
	if creds.TargetPath != "" && pathExists(creds.TargetPath) {
		infof("removing target path...")
		if out, err := run("rm", "-rf", creds.TargetPath); err != nil {
			warnf("rm target path failed: %v\n%s", err, out)
		}
	}

	// Remove database + user
	if creds.TargetDB.Name != "" {
		infof("removing database...")
		if out, err := run("/usr/sbin/plesk", "bin", "database", "--remove", creds.TargetDB.Name); err != nil {
			warnf("database removal failed: %v\n%s", err, out)
		}
	}
	if creds.TargetDB.User != "" {
		// DB user is removed together with the database in Plesk usually
		_, _ = run("/usr/sbin/plesk", "bin", "database", "--remove-dbuser", creds.TargetDB.User)
	}

	// Remove credentials file
	if pathExists(credsPath) {
		if err := os.Remove(credsPath); err != nil {
			warnf("could not remove credentials file: %v", err)
		}
	}

	infof("✓ staging removed")
}
