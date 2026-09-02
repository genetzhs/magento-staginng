package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// vhostsRoot returns the Plesk vhosts directory (HTTPD_VHOSTS_D from
// /etc/psa/psa.conf), falling back to /var/www/vhosts.
func vhostsRoot() string {
	out, err := run("grep", "-E", "^HTTPD_VHOSTS_D[[:space:]]+", "/etc/psa/psa.conf")
	if err != nil {
		return "/var/www/vhosts"
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "HTTPD_VHOSTS_D") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(fields[1], "/") {
			return strings.TrimRight(fields[1], "/")
		}
	}
	return "/var/www/vhosts"
}

// vhostAncestor returns the subscription (webspace) root directory for a
// path below the Plesk vhosts directory: the first path component below
// the vhosts root. The directory under vhosts is named after the
// subscription's main domain, which is not always the domain being staged
// (renamed subscriptions keep their old directory name; secondary domains
// live in the main domain's directory).
//
// Example:
//
//	vhostAncestor("/var/www/vhosts", "/var/www/vhosts/main.example.com/httpdocs")
//	  -> "/var/www/vhosts/main.example.com"
//
// Returns "" when the path is not below the vhosts root.
func vhostAncestor(vroot, p string) string {
	p = strings.TrimRight(p, "/")
	if p == vroot {
		return vroot
	}
	if !strings.HasPrefix(p, vroot+"/") {
		return ""
	}
	rest := p[len(vroot)+1:]
	name := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		name = rest[:i]
	}
	if name == "" {
		return ""
	}
	return vroot + "/" + name
}

// magentoRootNear resolves the Magento project root around a Plesk document
// root. Both layouts are handled:
//   - document root == Magento root (app/, pub/ directly inside it)
//   - document root == <Magento root>/pub (recommended Magento 2 setup)
//
// Returns "" when neither the document root nor its parent contains
// app/etc/env.php.
func magentoRootNear(docroot string) string {
	docroot = strings.TrimRight(docroot, "/")
	if docroot == "" {
		return ""
	}
	if pathExists(docroot + "/app/etc/env.php") {
		return docroot
	}
	parent := filepath.Dir(docroot)
	if parent != docroot && pathExists(parent+"/app/etc/env.php") {
		return parent
	}
	return ""
}

// findMagentoRoot locates a Magento installation under dir: the
// conventional httpdocs/ first, then direct subdirectories containing
// app/etc/env.php (custom document roots, subdomain directories). The
// staging target directory and the credentials directory are skipped so a
// previous staging copy is never picked as the source.
func findMagentoRoot(c *config, dir string) string {
	if pathExists(dir + "/httpdocs/app/etc/env.php") {
		return dir + "/httpdocs"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == "httpdocs" || e.Name() == c.stagingName || e.Name() == ".credentials" {
			continue
		}
		p := dir + "/" + e.Name()
		if pathExists(p + "/app/etc/env.php") {
			return p
		}
	}
	return ""
}

// webspaceRootForDomain resolves the subscription (webspace) root
// directory for a domain by asking Plesk for its document root. Used by
// commands that need the credentials location without a full create run
// (info / cleanup).
func webspaceRootForDomain(domain string) string {
	vroot := vhostsRoot()
	if docroot, err := pleskDocumentRoot(domain); err == nil && docroot != "" {
		if root := vhostAncestor(vroot, docroot); root != "" {
			return root
		}
	}
	return vroot + "/" + domain
}

// credsPathFor returns the credentials file path for a domain/staging
// pair, resolving the webspace root via Plesk.
func credsPathFor(domain, stagingName string) string {
	return webspaceRootForDomain(domain) + "/.credentials/" + stagingName + ".json"
}

// resolvePaths determines the webspace root, the Magento source root and
// the staging target / credentials paths for a create run.
//
// Source resolution order:
//  1. explicit --source-path flag (webspace root derived from it)
//  2. the document root Plesk serves for the live domain (psa hosting
//     table, domain aliases followed), with the Magento root at or above it
//  3. conventional <webspace>/httpdocs, then a scan of the webspace
//     directories for a Magento installation
//
// All derived paths (staging target, credentials, log) are placed under
// the real webspace root, which may differ from /var/www/vhosts/<domain>
// when the subscription directory is named after another domain.
func (c *config) resolvePaths() error {
	vroot := vhostsRoot()

	// Plesk-side subscription name for the live domain (equals --domain
	// for main domains; the subscription's main domain for secondary
	// domains).
	c.webspaceName = pleskWebspaceName(c.domain)

	// PHP handler of the live domain — used both to select the CLI binary
	// and to configure the staging subdomain with the same PHP version.
	c.phpHandlerID = pleskPHPHandlerID(c.domain)
	if c.phpHandlerID != "" {
		c.verbosef("plesk php handler for %s: %s", c.domain, c.phpHandlerID)
	}

	docroot, derr := pleskDocumentRoot(c.domain)
	if derr != nil {
		c.verbosef("plesk document root lookup for %s failed: %v", c.domain, derr)
	} else {
		c.verbosef("plesk document root for %s: %s", c.domain, docroot)
	}

	switch {
	case c.sourcePath != "":
		// Explicit --source-path: derive the webspace root from it.
		c.webspaceRoot = vhostAncestor(vroot, c.sourcePath)
		c.verbosef("using --source-path: %s", c.sourcePath)
	case docroot != "":
		c.sourceDocRoot = docroot
		c.webspaceRoot = vhostAncestor(vroot, docroot)
		if root := magentoRootNear(docroot); root != "" {
			c.sourcePath = root
			c.verbosef("magento root from plesk document root: %s", root)
		} else {
			c.verbosef("no app/etc/env.php in or above document root %s", docroot)
		}
	}
	if c.webspaceRoot == "" {
		c.webspaceRoot = vroot + "/" + c.domain
	}

	// Fallback: locate the Magento root by convention / scan.
	if c.sourcePath == "" {
		if root := findMagentoRoot(c, c.webspaceRoot); root != "" {
			c.sourcePath = root
			c.verbosef("magento root from webspace scan: %s", root)
		} else {
			detail := fmt.Sprintf("plesk document root: %s", docroot)
			if derr != nil {
				detail = fmt.Sprintf("plesk document root lookup failed: %v", derr)
			}
			return fmt.Errorf("could not locate the Magento installation for %s under %s (%s) — pass --source-path /path/to/magento",
				c.domain, c.webspaceRoot, detail)
		}
	}

	// Staging target, credentials and log all live under the webspace root.
	if c.targetPath == "" {
		c.targetPath = c.webspaceRoot + "/" + c.stagingName
	}
	if c.credsPath == "" {
		c.credsPath = c.webspaceRoot + "/.credentials/" + c.stagingName + ".json"
	}
	return nil
}
