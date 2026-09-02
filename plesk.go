package main

import (
	"fmt"
	"strings"
)

// pleskPHPHandlerID returns the Plesk PHP handler configured for the
// domain (e.g. "plesk-php74-fpm"), or "" when it cannot be resolved.
func pleskPHPHandlerID(domain string) string {
	safe, err := safeDomainSQL(domain)
	if err != nil {
		return ""
	}
	q := "SELECT php_handler_id FROM hosting h JOIN domains d ON d.id = h.dom_id " +
		"WHERE d.name = '" + safe + "'"
	id, err := pleskDBQuery(q)
	if err != nil {
		return ""
	}
	return id
}

// phpBinFromHandlerID maps a Plesk PHP handler id like "plesk-php74-fpm"
// to the Plesk multi-PHP CLI binary path (e.g. /opt/plesk/php/7.4/bin/php).
// Returns "" for handlers without a recognizable version (e.g. OS PHP).
func phpBinFromHandlerID(id string) string {
	for _, seg := range strings.Split(id, "-") {
		if !strings.HasPrefix(seg, "php") {
			continue
		}
		digits := seg[len("php"):]
		if digits == "" {
			return ""
		}
		for _, r := range digits {
			if r < '0' || r > '9' {
				return ""
			}
		}
		// "74" -> "7.4", "83" -> "8.3", "56" -> "5.6"
		ver := digits[:len(digits)-1] + "." + digits[len(digits)-1:]
		return "/opt/plesk/php/" + ver + "/bin/php"
	}
	return ""
}

// pleskSubdomainCreate creates a subdomain via Plesk CLI with a custom www-root.
// www-root is relative to the subscription root. We use stagingName/pub/ as
// the document root (Magento 2 recommended setup). The document root MUST be
// pub/ because:
//   - Magento 2's public entry point is pub/index.php
//   - Static assets (CSS/JS/fonts) live in pub/static/ and are served at /static/
//   - If docroot is staging/ (not staging/pub/), nginx serves /static/ from
//     staging/static/ which doesn't exist (the files are in staging/pub/static/)
//   - Plesk's nginx serves static files directly, bypassing Apache's .htaccess
//     rewrite rules (source site works because it proxies ALL requests to Apache)
//
// Files still live in staging/ (bin/, app/, vendor/, pub/, etc.) — only the
// docroot points to the pub/ subdirectory, which is the secure Magento 2 setup
// (app/, vendor/, etc. are not web-accessible).
func pleskSubdomainCreate(c *config) error {
	// Prefer the live domain's PHP handler so the staging subdomain
	// serves the same PHP version as production (e.g. an old Magento
	// cannot run on the subscription default if that is newer).
	handler := c.phpHandlerID
	if c.dryRun {
		infof("  [dry-run] plesk bin subdomain --create %s.%s -webspace-name %s -www-root %s/pub/ -php true -php_handler_id %s -ssl true",
			c.stagingName, c.domain, c.webspaceName, c.stagingName, handler)
		return nil
	}
	args := []string{
		"/usr/sbin/plesk", "bin", "subdomain", "--create",
		c.stagingName + "." + c.domain,
		"-webspace-name", c.webspaceName,
		"-www-root", c.stagingName + "/pub/",
		"-php", "true",
	}
	if handler != "" {
		args = append(args, "-php_handler_id", handler)
	}
	args = append(args, "-ssl", "true")
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
//
// The -domain flag must receive the subscription's MAIN domain
// (c.webspaceName), not a secondary domain: databases belong to the
// subscription and Plesk rejects creation with "This object can be
// created only in a subscription" when given a secondary domain.
func pleskDatabaseCreate(c *config) error {
	if c.dryRun {
		infof("  [dry-run] plesk bin database --create %s -domain %s -type mysql",
			c.targetDB, c.webspaceName)
		return nil
	}
	args := []string{
		"/usr/sbin/plesk", "bin", "database", "--create",
		c.targetDB,
		"-domain", c.webspaceName,
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
			c.targetDBUser, c.webspaceName, c.targetDB)
		return nil
	}
	args := []string{
		"/usr/sbin/plesk", "bin", "database", "--create-dbuser",
		c.targetDBUser,
		"-domain", c.webspaceName,
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

// pleskDatabaseExists checks if the database already exists.
// `plesk bin database --info` is not a valid command on all Plesk
// versions ("Unknown command"), so the authoritative check is a
// SHOW DATABASES query through `plesk db`.
func pleskDatabaseExists(c *config) bool {
	safe, err := safeDomainSQL(c.targetDB)
	if err == nil {
		name, qerr := pleskDBQuery("SHOW DATABASES LIKE '" + safe + "'")
		if qerr == nil {
			return name != ""
		}
	}
	// Fall back to the old CLI form (works on some versions).
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
//
//	plesk bin extension --exec letsencrypt cli.php -d <subdomain>
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

// safeDomainSQL validates a domain name before embedding it into a psa
// database query (defense in depth: only [A-Za-z0-9.-_] are allowed).
func safeDomainSQL(domain string) (string, error) {
	if domain == "" {
		return "", fmt.Errorf("empty domain")
	}
	for _, r := range domain {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_':
		default:
			return "", fmt.Errorf("invalid character %q in domain %q", r, domain)
		}
	}
	return domain, nil
}

// pleskDBQuery runs a read-only SQL query against the Plesk `psa` database
// via `plesk db` and returns the first field of the last result row.
func pleskDBQuery(query string) (string, error) {
	out, err := run("/usr/sbin/plesk", "db", query)
	if err != nil {
		return "", fmt.Errorf("plesk db failed: %v\n%s", err, out)
	}
	return dbFirstField(out), nil
}

// dbFirstField extracts the first column of the last data row of `plesk
// db` output. It handles both output shapes:
//   - plain tab-separated rows ("value1\tvalue2")
//   - mysql ASCII tables ("+----+" borders, "| col | col |" rows)
//
// Border lines are skipped; the header row is naturally superseded by the
// data row that follows it (the LAST field wins).
func dbFirstField(out string) string {
	last := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "+") {
			continue
		}
		if strings.HasPrefix(line, "|") {
			line = strings.Trim(line, "| ")
		}
		field := line
		if i := strings.IndexAny(line, "\t|"); i >= 0 {
			field = line[:i]
		}
		if field = strings.TrimSpace(field); field != "" {
			last = field
		}
	}
	return last
}

// pleskDocumentRoot returns the document root Plesk serves for the given
// domain — the authoritative location of the live site, which may differ
// from /var/www/vhosts/<domain>/httpdocs (custom document roots, renamed
// subscriptions, secondary domains). Domain aliases are followed.
func pleskDocumentRoot(domain string) (string, error) {
	safe, err := safeDomainSQL(domain)
	if err != nil {
		return "", err
	}
	q := "SELECT h.www_root FROM hosting h JOIN domains d ON d.id = h.dom_id " +
		"WHERE d.name = '" + safe + "'"
	root, err := pleskDBQuery(q)
	if err != nil {
		return "", err
	}
	if isDocRootPath(root) {
		return root, nil
	}
	// Not hosted under its own name — maybe a domain alias.
	q = "SELECT h.www_root FROM domain_aliases a " +
		"JOIN domains d ON d.id = a.dom_id " +
		"JOIN hosting h ON h.dom_id = d.id " +
		"WHERE a.name = '" + safe + "'"
	root, err = pleskDBQuery(q)
	if err != nil {
		return "", err
	}
	if isDocRootPath(root) {
		return root, nil
	}
	return "", fmt.Errorf("no hosting entry found for %s in the Plesk database", domain)
}

// isDocRootPath reports whether s looks like an absolute filesystem path
// as returned for hosting.www_root (guards against parse hiccups where a
// column header would surface as the value).
func isDocRootPath(s string) bool {
	return strings.HasPrefix(s, "/")
}

// pleskWebspaceName returns the name of the subscription (webspace) that
// contains the domain: the domain itself when it is the subscription's
// main domain, otherwise the main domain's name. Falls back to the given
// domain when the lookup is not possible.
func pleskWebspaceName(domain string) string {
	safe, err := safeDomainSQL(domain)
	if err != nil {
		return domain
	}
	q := "SELECT wd.name FROM domains d JOIN domains wd ON wd.id = d.webspace_id " +
		"WHERE d.name = '" + safe + "'"
	name, err := pleskDBQuery(q)
	if err != nil || !strings.Contains(name, ".") {
		return domain
	}
	return name
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
