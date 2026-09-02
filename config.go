package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// globalConfig is the active config (set by main) so that package-level
// helpers infof/warnf can write to the log file.
var globalConfig = &config{}

// config holds all resolved settings for a create run.
type config struct {
	// Input flags
	domain               string
	stagingName          string
	sourcePath           string
	targetPath           string
	sourceDB             string
	targetDB             string
	targetDBUser         string
	targetDBPass         string
	redisIDPrefix        string
	elasticSuffix        string
	basicAuthUser        string
	basicAuthPass        string
	phpBin               string
	magentoMode          string
	schemaOnlyFile       string
	noSchemaOnly         bool
	includeGit           bool
	skipSSL              bool
	skipBasicAuth        bool
	skipPaymentDisable   bool
	skipAnalyticsDisable bool
	skipCronDisable      bool
	skipEmailDisable     bool
	skipSEODisable       bool
	noSalesData          bool
	noCustomerData       bool
	dryRun               bool
	nonInteractive       bool
	verbose              bool
	logFile              string // path to write a copy of all tool output

	// Resolved at runtime
	sourceDBUser         string
	sourceDBPass         string
	sourceDBHost         string
	sysUser              string // Plesk system user for the subscription
	webspaceRoot         string // subscription root dir, e.g. /var/www/vhosts/<webspace>
	webspaceName         string // subscription name as Plesk knows it (for -webspace-name)
	phpHandlerID         string // Plesk PHP handler of the live domain (e.g. plesk-php74-fpm)
	sourceDocRoot        string // document root Plesk serves for the live domain
	sourceMageMode       string
	originalRedisPrefix  string
	originalES6Prefix    string
	originalES7Prefix    string
	originalAmastyPrefix string
	sourceAdminFrontName string
	credsPath            string
	logWriter            *os.File // opened log file handle
}

// derive fills in derived/defaults after raw flags are parsed. Paths that
// depend on the Plesk layout (source, target, credentials) are resolved
// later by resolvePaths().
func (c *config) derive() error {
	if c.stagingName == "" {
		c.stagingName = "staging"
	}
	if c.phpBin == "" {
		c.phpBin = "/usr/bin/php"
	}
	if c.elasticSuffix == "" {
		c.elasticSuffix = "stg"
	}
	if c.basicAuthUser == "" {
		c.basicAuthUser = "admin"
	}
	return nil
}

// stagingURL returns the public staging URL.
func (c *config) stagingURL() string {
	return "https://" + c.stagingName + "." + c.domain + "/"
}

// verbosef prints only when --verbose.
func (c *config) verbosef(format string, args ...interface{}) {
	if c.verbose {
		fmt.Fprintf(os.Stderr, "%s[verbose]%s %s\n",
			colorWhite, colorReset, fmt.Sprintf(format, args...))
	}
	c.logWritef("[verbose] "+format, args...)
}

// infof prints always (informational progress).
func infof(format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, format, args...)
	globalConfig.logWritef(format, args...)
}

// logWritef writes a line to the log file if one is open.
func (c *config) logWritef(format string, args ...interface{}) {
	if c.logWriter == nil {
		return
	}
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(c.logWriter, format, args...)
}

// openLog opens (or creates) the log file for appending and wires it so
// that stdout/stderr of child commands is also captured there. The log
// is line-buffered and flushed on every write.
func (c *config) openLog() error {
	if c.logFile == "" {
		return nil
	}
	// Ensure parent dir exists
	dir := c.logFile[:strings.LastIndexByte(c.logFile, '/')]
	if dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	f, err := os.OpenFile(c.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return err
	}
	c.logWriter = f
	c.logWritef("=== magento-staging run %s ===", time.Now().Format(time.RFC3339))
	return nil
}

// closeLog flushes and closes the log file if open.
func (c *config) closeLog() {
	if c.logWriter != nil {
		c.logWritef("=== end of run ===")
		_ = c.logWriter.Close()
	}
}

// stepf prints a numbered step header (kept for backward compat).
// Deprecated: use printStep() instead.
func stepf(n int, total int, format string, args ...interface{}) {
	printStep(n, total, fmt.Sprintf(format, args...))
}

// warnf prints a warning.
func warnf(format string, args ...interface{}) {
	printWarn(format, args...)
}

// failf prints an error and exits 1.
func failf(format string, args ...interface{}) {
	printFail(format, args...)
	os.Exit(1)
}
