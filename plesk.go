package main

import (
	"fmt"
	"strings"
)

// pleskSubdomainCreate creates a subdomain via Plesk CLI with a custom www-root.
// www-root is relative to the subscription root and MUST include the httpdocs
// suffix so that the document root matches our rsync target
// (targetPath + "/httpdocs/").
func pleskSubdomainCreate(c *config) error {
	if c.dryRun {
		infof("  [dry-run] plesk bin subdomain --create %s.%s -webspace-name %s -www-root %s/httpdocs/ -php true",
			c.stagingName, c.domain, c.domain, c.stagingName)
		return nil
	}
	args := []string{
		"/usr/sbin/plesk", "bin", "subdomain", "--create",
		c.stagingName + "." + c.domain,
		"-webspace-name", c.domain,
		"-www-root", c.stagingName + "/httpdocs/",
		"-php", "true",
		"-ssl", "true",
	}
	out, err := run(args[0], args[1:]...)
	if err != nil {
		return fmt.Errorf("plesk subdomain create failed: %v\n%s", err, out)
	}
	c.verbosef("plesk subdomain created: %s", strings.TrimSpace(out))
	return nil
}

// pleskSubdomainExists checks if the subdomain already exists.
func pleskSubdomainExists(c *config) bool {
	args := []string{
		"/usr/sbin/plesk", "bin", "subdomain", "--info",
		c.stagingName + "." + c.domain,
	}
	_, err := run(args[0], args[1:]...)
	return err == nil
}

// pleskSubdomainRemove removes the subdomain.
func pleskSubdomainRemove(c *config) error {
	if c.dryRun {
		infof("  [dry-run] plesk bin subdomain --remove %s.%s", c.stagingName, c.domain)
		return nil
	}
	args := []string{
		"/usr/sbin/plesk", "bin", "subdomain", "--remove",
		c.stagingName + "." + c.domain,
	}
	out, err := run(args[0], args[1:]...)
	if err != nil {
		return fmt.Errorf("plesk subdomain remove failed: %v\n%s", err, out)
	}
	return nil
}

// pleskDatabaseCreate creates a database in Plesk.
func pleskDatabaseCreate(c *config) error {
	if c.dryRun {
		infof("  [dry-run] plesk bin database --create %s -domain %s -type mysql",
			c.targetDB, c.domain)
		return nil
	}
	args := []string{
		"/usr/sbin/plesk", "bin", "database", "--create",
		c.targetDB,
		"-domain", c.domain,
		"-type", "mysql",
	}
	out, err := run(args[0], args[1:]...)
	if err != nil {
		return fmt.Errorf("plesk database create failed: %v\n%s", err, out)
	}
	return nil
}

// pleskDatabaseUserCreate creates a DB user with access to the target DB.
func pleskDatabaseUserCreate(c *config) error {
	if c.dryRun {
		infof("  [dry-run] plesk bin database --create-dbuser %s -domain %s -type mysql -database %s -passwd ***",
			c.targetDBUser, c.domain, c.targetDB)
		return nil
	}
	args := []string{
		"/usr/sbin/plesk", "bin", "database", "--create-dbuser",
		c.targetDBUser,
		"-domain", c.domain,
		"-type", "mysql",
		"-database", c.targetDB,
		"-passwd", c.targetDBPass,
	}
	out, err := run(args[0], args[1:]...)
	if err != nil {
		return fmt.Errorf("plesk db user create failed: %v\n%s", err, out)
	}
	return nil
}

// pleskDatabaseExists checks if the database already exists in Plesk.
func pleskDatabaseExists(c *config) bool {
	out, err := run("/usr/sbin/plesk", "bin", "database", "--info", c.targetDB)
	return err == nil && !strings.Contains(strings.ToLower(out), "does not exist")
}

// pleskDatabaseRemove removes the database.
func pleskDatabaseRemove(c *config) error {
	if c.dryRun {
		infof("  [dry-run] plesk bin database --remove %s", c.targetDB)
		return nil
	}
	args := []string{
		"/usr/sbin/plesk", "bin", "database", "--remove",
		c.targetDB,
	}
	out, err := run(args[0], args[1:]...)
	if err != nil {
		return fmt.Errorf("plesk database remove failed: %v\n%s", err, out)
	}
	return nil
}

// pleskIssueSSL requests a Let's Encrypt certificate for the staging subdomain.
//
// Plesk's letsencrypt extension CLI is invoked via:
//   plesk bin extension --exec letsencrypt cli.php -d <subdomain>
//
// We pass only the staging subdomain (not the main domain) because the main
// domain already has its own cert. Let's Encrypt will issue a cert for the
// subdomain and Plesk will install it automatically.
func pleskIssueSSL(c *config) error {
	if c.dryRun {
		infof("  [dry-run] plesk bin extension --exec letsencrypt cli.php -d %s.%s",
			c.stagingName, c.domain)
		return nil
	}
	args := []string{
		"/usr/sbin/plesk", "bin", "extension", "--exec", "letsencrypt",
		"cli.php",
		"-d", c.stagingName + "." + c.domain,
	}
	out, err := run(args[0], args[1:]...)
	if err != nil {
		return fmt.Errorf("plesk ssl issuance failed: %v\n%s", err, out)
	}
	return nil
}

// pleskProtectedURLCreate protects the site root with HTTP Basic Auth.
// Falls back to writing .htaccess directly if Plesk command fails.
func pleskProtectedURLCreate(c *config, htpasswdPath string) error {
	if c.dryRun {
		infof("  [dry-run] plesk bin protected_url --create / -domain %s.%s -user %s -passwd ***",
			c.stagingName, c.domain, c.basicAuthUser)
		return nil
	}
	args := []string{
		"/usr/sbin/plesk", "bin", "protected_url", "--create", "/",
		"-domain", c.stagingName + "." + c.domain,
		"-user", c.basicAuthUser,
		"-passwd", c.basicAuthPass,
	}
	out, err := run(args[0], args[1:]...)
	if err != nil {
		c.verbosef("plesk protected_url failed, will use .htaccess fallback: %v\n%s", err, out)
		return writeHtaccessFallback(c, htpasswdPath)
	}
	return nil
}

// pleskSiteInfo retrieves the system user for a domain/subscription.
func pleskSiteInfo(domain string) (sysUser string, err error) {
	out, err := run("/usr/sbin/plesk", "bin", "site", "--info", domain)
	if err != nil {
		return "", fmt.Errorf("plesk site --info failed: %v\n%s", err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// Plesk Obsidian uses "FTP Login:" for the subscription system user.
		if strings.HasPrefix(line, "FTP Login:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
		// Older Plesk versions used "Login:"
		if strings.HasPrefix(line, "Login:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}
	return "", fmt.Errorf("could not find system user in plesk site --info output")
}

// pleskListDomains returns all domains hosted on this Plesk server.
func pleskListDomains() ([]string, error) {
	out, err := run("/usr/sbin/plesk", "bin", "site", "--list")
	if err != nil {
		return nil, fmt.Errorf("plesk site --list failed: %v\n%s", err, out)
	}
	var domains []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "List of") {
			domains = append(domains, line)
		}
	}
	return domains, nil
}
