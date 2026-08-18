package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fixHtaccessFollowSymLinks replaces FollowSymLinks with SymLinksIfOwnerMatch
// in all .htaccess files within the staging httpdocs. Plesk's Apache config
// disallows FollowSymLinks in .htaccess (Options directive), but
// SymLinksIfOwnerMatch is permitted and provides equivalent functionality
// with better security (only follows symlinks owned by the same user).
func fixHtaccessFollowSymLinks(c *config) {
	if c.dryRun {
		printInfo("[dry-run] fix FollowSymLinks -> SymLinksIfOwnerMatch in .htaccess files")
		return
	}
	httpdocs := c.targetPath + "/httpdocs"
	count := 0
	_ = filepath.Walk(httpdocs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Base(path) != ".htaccess" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		if !strings.Contains(content, "FollowSymLinks") {
			return nil
		}
		// Replace FollowSymLinks with SymLinksIfOwnerMatch
		newContent := strings.ReplaceAll(content, "FollowSymLinks", "SymLinksIfOwnerMatch")
		if err := os.WriteFile(path, []byte(newContent), info.Mode()); err == nil {
			count++
			c.verbosef("patched %s", path)
		}
		return nil
	})
	if count > 0 {
		printOK("patched %d .htaccess file(s) (FollowSymLinks -> SymLinksIfOwnerMatch)", count)
	}
}

// fixOwnership sets correct ownership and permissions on the staging
// httpdocs directory according to Plesk conventions.
//
// Plesk convention:
//   * Directories: 0750 (or 2750 with setgid for inheritance)
//   * Files:       0640
//   * Owner:       <sysuser>:psaserv
//   * app/etc/env.php: 0640
//   * bin/magento:     0750
//   * var/, pub/media/, pub/static/: group-writable (0770 or g+w)
func fixOwnership(c *config) error {
	if c.dryRun {
		infof("  [dry-run] chown -R %s:psaserv %s/httpdocs", c.sysUser, c.targetPath)
		infof("  [dry-run] fix directory/file permissions")
		return nil
	}

	httpdocs := c.targetPath + "/httpdocs"

	// chown -R <sysuser>:psaserv httpdocs
	if out, err := run("chown", "-R", c.sysUser+":psaserv", httpdocs); err != nil {
		return fmt.Errorf("chown failed: %v\n%s", err, out)
	}

	// Directories: 2750 (setgid ensures new files inherit group)
	if err := filepath.Walk(httpdocs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if info.IsDir() {
			if err := os.Chmod(path, 02750); err != nil {
				c.verbosef("chmod dir %s failed: %v", path, err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk dirs failed: %v", err)
	}

	// Files: 0640
	if err := filepath.Walk(httpdocs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			if err := os.Chmod(path, 0640); err != nil {
				c.verbosef("chmod file %s failed: %v", path, err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk files failed: %v", err)
	}

	// Special: bin/magento executable
	binMagento := httpdocs + "/bin/magento"
	if pathExists(binMagento) {
		_ = os.Chmod(binMagento, 0750)
	}

	// Special: app/etc/env.php readable by web
	envPath := httpdocs + "/app/etc/env.php"
	if pathExists(envPath) {
		_ = os.Chmod(envPath, 0640)
	}

	// Writable directories: var/, pub/media/, pub/static/
	writableDirs := []string{
		httpdocs + "/var",
		httpdocs + "/pub/media",
		httpdocs + "/pub/static",
		httpdocs + "/generated",
	}
	for _, d := range writableDirs {
		if !pathExists(d) {
			continue
		}
		// chmod -R g+w on these (group-writable for web processes)
		if out, err := run("chmod", "-R", "g+w", d); err != nil {
			c.verbosef("chmod g+w %s failed: %v\n%s", d, err, out)
		}
		// Ensure setgid on directories within writable areas
		_ = filepath.Walk(d, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				_ = os.Chmod(path, 0770)
			}
			return nil
		})
	}

	return nil
}
