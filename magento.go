package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// magentoCLI runs a bin/magento command as the system user in the STAGING
// httpdocs directory (so it picks up the patched env.php).
func magentoCLI(c *config, args ...string) (string, error) {
	stagingHttpdocs := c.targetPath
	full := append([]string{c.phpBin, "bin/magento"}, args...)
	return runAsUser(c, c.sysUser, stagingHttpdocs, full[0], full[1:]...)
}

// magentoSetupUpgrade runs setup:upgrade.
func magentoSetupUpgrade(c *config) error {
	if c.dryRun {
		printInfo("[dry-run] bin/magento setup:upgrade (as %s in %s)", c.sysUser, c.targetPath)
		return nil
	}
	if err := ensureRDBMSAllowed(c); err != nil {
		warnf("DB version check in di.xml skipped: %v", err)
	}
	sp := newSpinner("Running setup:upgrade")
	sp.Start()
	out, err := magentoCLI(c, "setup:upgrade")
	if err != nil {
		if ver, ok := unsupportedRDBMSVersion(out); ok {
			sp.Stop(false, fmt.Sprintf("setup:upgrade rejected DB server version %s", ver))
			if perr := allowRDBMSVersion(c, ver); perr != nil {
				return fmt.Errorf("setup:upgrade failed: %v\n%s\n(could not whitelist %s: %v)", err, tail(out, 20), ver, perr)
			}
			printInfo("whitelisted %s in staging app/etc/di.xml, retrying", ver)
			sp = newSpinner("Running setup:upgrade (retry)")
			sp.Start()
			out, err = magentoCLI(c, "setup:upgrade")
		}
	}
	if err != nil {
		sp.Stop(false, fmt.Sprintf("setup:upgrade failed: %v", err))
		return fmt.Errorf("setup:upgrade failed: %v\n%s", err, tail(out, 20))
	}
	sp.Stop(true, "setup:upgrade completed")
	c.verbosef("setup:upgrade output: %s", tail(out, 10))
	return nil
}

// magentoDICompile runs setup:di:compile (generates DI code, required in
// production mode). Per Adobe docs: after URL change in production mode,
// generated/code/ must be rebuilt. Skipped in developer mode.
func magentoDICompile(c *config) error {
	if c.dryRun {
		printInfo("[dry-run] bin/magento setup:di:compile")
		return nil
	}
	sp := newSpinner("Compiling DI (setup:di:compile)")
	sp.Start()
	out, err := magentoCLI(c, "setup:di:compile")
	if err != nil {
		sp.Stop(false, fmt.Sprintf("setup:di:compile failed: %v", err))
		warnf("setup:di:compile had errors (continuing): %v\n%s", err, tail(out, 20))
		return nil // not fatal
	}
	sp.Stop(true, "DI compilation completed")
	return nil
}

// magentoDeployStaticContent runs setup:static-content:deploy.
// Per Adobe docs: after a URL change, static assets (CSS/JS/images/fonts)
// must be redeployed to pub/static/ with the new signed version path,
// otherwise the frontend has 404s on all CSS/JS (broken layout).
func magentoDeployStaticContent(c *config) error {
	if c.dryRun {
		printInfo("[dry-run] bin/magento setup:static-content:deploy -f el_GR en_US")
		return nil
	}
	sp := newSpinner("Deploying static content (setup:static-content:deploy)")
	sp.Start()
	out, err := magentoCLI(c, "setup:static-content:deploy", "-f", "el_GR", "en_US")
	if err != nil {
		sp.Stop(false, fmt.Sprintf("static-content:deploy failed: %v", err))
		warnf("setup:static-content:deploy had errors: %v\n%s", err, tail(out, 20))
		return nil // not fatal, but site will look broken
	}
	sp.Stop(true, "static content deployed (el_GR, en_US)")
	return nil
}

// magentoCacheFlush runs cache:flush.
func magentoCacheFlush(c *config) error {
	if c.dryRun {
		printInfo("[dry-run] bin/magento cache:flush")
		return nil
	}
	sp := newSpinner("Flushing cache")
	sp.Start()
	out, err := magentoCLI(c, "cache:flush")
	if err != nil {
		sp.Stop(false, fmt.Sprintf("cache:flush failed: %v", err))
		return fmt.Errorf("cache:flush failed: %v\n%s", err, tail(out, 20))
	}
	sp.Stop(true, "cache flushed")
	return nil
}

// magentoReindex runs indexer:reindex.
func magentoReindex(c *config) error {
	if c.dryRun {
		printInfo("[dry-run] bin/magento indexer:reindex")
		return nil
	}
	sp := newSpinner("Reindexing (this can take a few minutes)")
	sp.Start()
	out, err := magentoCLI(c, "indexer:reindex")
	if err != nil {
		sp.Stop(false, fmt.Sprintf("indexer:reindex had errors: %v", err))
		warnf("indexer:reindex had errors (continuing): %v\n%s", err, tail(out, 20))
		return nil // not fatal - some indexers may fail without all data
	}
	sp.Stop(true, "reindex completed")
	return nil
}

// magentoDeployMode runs deploy:mode:set if --magento-mode was specified.
func magentoDeployMode(c *config) error {
	if c.magentoMode == "" || c.dryRun {
		return nil
	}
	sp := newSpinner(fmt.Sprintf("Setting MAGE_MODE=%s", c.magentoMode))
	sp.Start()
	out, err := magentoCLI(c, "deploy:mode:set", c.magentoMode)
	if err != nil {
		sp.Stop(false, fmt.Sprintf("deploy:mode:set failed: %v", err))
		return fmt.Errorf("deploy:mode:set failed: %v\n%s", err, tail(out, 20))
	}
	sp.Stop(true, fmt.Sprintf("MAGE_MODE set to %s", c.magentoMode))
	return nil
}

// magentoMaintenanceDisable disables maintenance mode.
func magentoMaintenanceDisable(c *config) error {
	if c.dryRun {
		printInfo("[dry-run] bin/magento maintenance:disable")
		return nil
	}
	out, err := magentoCLI(c, "maintenance:disable")
	if err != nil {
		return fmt.Errorf("maintenance:disable failed: %v\n%s", err, tail(out, 20))
	}
	printOK("maintenance mode disabled")
	return nil
}

// tail returns the last n lines of s.
func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// cleanMagentoGenerated clears the generated code & cache subdirectories.
// Per Adobe docs: after a URL change, generated/code/, var/cache/, and
// var/page_cache/ must be cleared before running setup:di:compile and
// setup:static-content:deploy, otherwise stale compiled code from the
// production URL may be served.
func cleanMagentoGenerated(c *config) {
	if c.dryRun {
		printInfo("[dry-run] clean generated/code, var/cache, var/page_cache")
		return
	}
	dirs := []string{
		c.targetPath + "/generated/code",
		c.targetPath + "/generated/metadata",
		c.targetPath + "/var/cache",
		c.targetPath + "/var/page_cache",
		c.targetPath + "/var/view_preprocessed",
	}
	for _, d := range dirs {
		if pathExists(d) {
			c.verbosef("cleaning %s", d)
			out, err := run("rm", "-rf", d)
			if err != nil {
				warnf("failed to clean %s: %v\n%s", d, err, out)
			}
		}
	}
}

var (
	rdbmsUnsupportedRe = regexp.MustCompile(`Current version of RDBMS is not supported\. Used Version: (\d+\.\d+\.\d+)-(MariaDB|MySQL)`)
	rdbmsLabelRe       = regexp.MustCompile(`^(MariaDB|MySQL)-(\d+)\.(\d+)$`)
	versionPatternsRe  = regexp.MustCompile(`(?s)<argument name="supportedVersionPatterns" xsi:type="array">(.*?)</argument>`)
	versionItemRe      = regexp.MustCompile(`<item [^>]*xsi:type="string">([^<]+)</item>`)
)

// unsupportedRDBMSVersion detects Magento's SqlVersionProvider rejection
// (e.g. MariaDB 10.11 on a Magento release that only lists 10.2-10.6) and
// returns a "MariaDB-10.11"-style label.
func unsupportedRDBMSVersion(out string) (string, bool) {
	m := rdbmsUnsupportedRe.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	return rdbmsLabel(m[1], m[2]), true
}

// rdbmsLabel turns "10.11.19" + "MariaDB" into "MariaDB-10.11".
func rdbmsLabel(version, flavor string) string {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	return fmt.Sprintf("%s-%s.%s", flavor, parts[0], parts[1])
}

// targetDBVersion returns the staging DB server version as Magento sees it
// (e.g. "10.11.19-MariaDB-log" -> "10.11.19", "MariaDB").
func targetDBVersion(c *config) (string, string, error) {
	out, err := run("mysql",
		"--user="+c.targetDBUser, "--password="+c.targetDBPass,
		"-h", c.targetDBHost, "-N", "-B", "-e", "SELECT VERSION();")
	if err != nil {
		return "", "", fmt.Errorf("SELECT VERSION() failed: %v\n%s", err, out)
	}
	v := strings.TrimSpace(out)
	m := regexp.MustCompile(`^(\d+\.\d+\.\d+)`).FindString(v)
	if m == "" {
		return "", "", fmt.Errorf("unparseable DB version %q", v)
	}
	flavor := "MySQL"
	if strings.Contains(strings.ToLower(v), "mariadb") {
		flavor = "MariaDB"
	}
	return m, flavor, nil
}

// versionAllowed reports whether version (e.g. "10.11.19") matches any of
// the supportedVersionPatterns in di.xml content.
func versionAllowed(diXML, version string) (bool, error) {
	block := versionPatternsRe.FindStringSubmatch(diXML)
	if block == nil {
		return false, fmt.Errorf("supportedVersionPatterns not found")
	}
	for _, it := range versionItemRe.FindAllStringSubmatch(block[1], -1) {
		re, err := regexp.Compile(strings.TrimSpace(it[1]))
		if err != nil {
			continue
		}
		if re.MatchString(version) {
			return true, nil
		}
	}
	return false, nil
}

// ensureRDBMSAllowed checks the staging DB server version against the
// staging app/etc/di.xml supportedVersionPatterns before setup:upgrade and
// whitelists it when missing (e.g. MariaDB 10.11 on Magento <= 2.4.6).
func ensureRDBMSAllowed(c *config) error {
	version, flavor, err := targetDBVersion(c)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(c.targetPath + "/app/etc/di.xml")
	if err != nil {
		return err
	}
	ok, err := versionAllowed(string(data), version)
	if err != nil {
		return err
	}
	if ok {
		c.verbosef("DB server %s %s already supported by di.xml", flavor, version)
		return nil
	}
	label := rdbmsLabel(version, flavor)
	if err := allowRDBMSVersion(c, label); err != nil {
		return err
	}
	printInfo("DB server %s %s not in di.xml supported versions: whitelisted %s in staging copy", flavor, version, label)
	return nil
}

// allowRDBMSVersion adds the DB server's major.minor to the
// supportedVersionPatterns of the STAGING copy's app/etc/di.xml. Only the
// staging tree is touched; production is never modified.
func allowRDBMSVersion(c *config, label string) error {
	m := rdbmsLabelRe.FindStringSubmatch(label)
	if m == nil {
		return fmt.Errorf("bad version label %q", label)
	}
	p := c.targetPath + "/app/etc/di.xml"
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	s := string(data)
	pattern := fmt.Sprintf(`^%s\.%s\.`, m[2], m[3])
	if strings.Contains(s, ">"+pattern+"<") {
		return fmt.Errorf("pattern %s already present", pattern)
	}
	loc := versionPatternsRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return fmt.Errorf("supportedVersionPatterns not found in %s", p)
	}
	item := fmt.Sprintf("\n                <item name=\"%s\" xsi:type=\"string\">%s</item>", label, pattern)
	at := loc[2] // start of the argument's inner content
	s = s[:at] + item + s[at:]
	// WriteFile on an existing file keeps owner and mode.
	return os.WriteFile(p, []byte(s), 0o644)
}
