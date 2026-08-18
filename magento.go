package main

import (
	"fmt"
	"strings"
)

// magentoCLI runs a bin/magento command as the system user in the STAGING
// httpdocs directory (so it picks up the patched env.php).
func magentoCLI(c *config, args ...string) (string, error) {
	stagingHttpdocs := c.targetPath + "/httpdocs"
	full := append([]string{c.phpBin, "bin/magento"}, args...)
	return runAsUser(c, c.sysUser, stagingHttpdocs, full[0], full[1:]...)
}

// magentoSetupUpgrade runs setup:upgrade.
func magentoSetupUpgrade(c *config) error {
	if c.dryRun {
		printInfo("[dry-run] bin/magento setup:upgrade (as %s in %s/httpdocs)", c.sysUser, c.targetPath)
		return nil
	}
	sp := newSpinner("Running setup:upgrade")
	sp.Start()
	out, err := magentoCLI(c, "setup:upgrade")
	if err != nil {
		sp.Stop(false, fmt.Sprintf("setup:upgrade failed: %v", err))
		return fmt.Errorf("setup:upgrade failed: %v\n%s", err, tail(out, 20))
	}
	sp.Stop(true, "setup:upgrade completed")
	c.verbosef("setup:upgrade output: %s", tail(out, 10))
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
