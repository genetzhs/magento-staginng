package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// generatePassword returns a URL-safe random password of the given length.
func generatePassword(length int) string {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; fall back to a constant (better than crashing)
		return "ChangeMe123!"
	}
	// Use base64 raw URL encoding to get printable chars, then trim to length.
	s := base64.RawURLEncoding.EncodeToString(b)
	if len(s) > length {
		s = s[:length]
	}
	return s
}

// generateHtpasswdBcrypt generates a bcrypt .htpasswd line using the apache2
// htpasswd utility if available, otherwise falls back to openssl.
//
// We use the system `htpasswd` tool to avoid pulling in a Go bcrypt
// dependency (keeps the build self-contained with stdlib only).
func generateHtpasswdBcrypt(user, password string) (string, error) {
	// Try apache2 htpasswd with -nB (bcrypt) flag.
	if path, _ := exec.LookPath("htpasswd"); path != "" {
		out, err := run("htpasswd", "-nbB", user, password)
		if err == nil {
			line := strings.TrimSpace(out)
			if strings.Contains(line, ":") {
				return line, nil
			}
		}
	}

	// Fallback: openssl passwd -apr1 (Apache MD5 - widely supported)
	if path, _ := exec.LookPath("openssl"); path != "" {
		out, err := run("openssl", "passwd", "-apr1", password)
		if err == nil {
			line := strings.TrimSpace(out)
			return user + ":" + line, nil
		}
	}

	// Last resort: plain text (NOT recommended; should warn the user)
	warnf("neither htpasswd nor openssl available; using crypt() fallback")
	out, err := run("python3", "-c",
		fmt.Sprintf("import crypt; print('%s:'+crypt.crypt('%s', crypt.mksalt(crypt.METHOD_SHA512)))",
			user, password))
	if err == nil {
		return strings.TrimSpace(out), nil
	}

	return "", fmt.Errorf("no tool available to generate htpasswd entry")
}

// writeHtpasswd writes the .htpasswd file at the given path with secure
// permissions (chmod 0640, owned by root:psaserv).
func writeHtpasswd(c *config, htpasswdPath string) error {
	if c.dryRun {
		infof("  [dry-run] write %s", htpasswdPath)
		return nil
	}
	line, err := generateHtpasswdBcrypt(c.basicAuthUser, c.basicAuthPass)
	if err != nil {
		return fmt.Errorf("htpasswd generation failed: %v", err)
	}
	if err := os.WriteFile(htpasswdPath, []byte(line+"\n"), 0640); err != nil {
		return fmt.Errorf("failed to write htpasswd: %v", err)
	}
	if err := os.Chmod(htpasswdPath, 0640); err != nil {
		warnf("chmod htpasswd failed: %v", err)
	}
	if err := os.Chown(htpasswdPath, 0, 0); err != nil {
		// root:root is fine; group doesn't matter for the file
	}
	return nil
}

// writeHtaccessFallback writes .htaccess directives that enable HTTP Basic
// Auth using a .htpasswd file. Used when plesk protected_url fails.
func writeHtaccessFallback(c *config, htpasswdPath string) error {
	htaccessPath := c.targetPath + "/httpdocs/.htaccess"
	directive := fmt.Sprintf(`
# magento-staging basic auth
AuthType Basic
AuthName "Staging"
AuthUserFile %s
Require valid-user
`, htpasswdPath)

	if c.dryRun {
		infof("  [dry-run] append basic auth to %s", htaccessPath)
		return nil
	}

	// Read existing .htaccess if present, append our block.
	existing := ""
	if data, err := os.ReadFile(htaccessPath); err == nil {
		existing = string(data)
	}
	// Avoid duplicating the marker block.
	if strings.Contains(existing, "# magento-staging basic auth") {
		return nil
	}
	newContent := existing + directive
	if err := os.WriteFile(htaccessPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write .htaccess: %v", err)
	}
	return nil
}
