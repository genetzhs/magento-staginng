package main

import (
	"fmt"
	"os"
	"strings"
)

// config holds all resolved settings for a create run.
type config struct {
	// Input flags
	domain            string
	stagingName       string
	sourcePath        string
	targetPath        string
	sourceDB          string
	targetDB          string
	targetDBUser      string
	targetDBPass      string
	redisIDPrefix     string
	elasticSuffix     string
	basicAuthUser     string
	basicAuthPass     string
	phpBin            string
	magentoMode       string
	schemaOnlyFile    string
	noSchemaOnly      bool
	includeGit        bool
	skipSSL           bool
	skipBasicAuth     bool
	skipPaymentDisable bool
	skipAnalyticsDisable bool
	skipCronDisable   bool
	skipEmailDisable  bool
	skipSEODisable    bool
	noSalesData       bool
	noCustomerData    bool
	dryRun            bool
	nonInteractive    bool
	verbose           bool

	// Resolved at runtime
	sourceDBUser     string
	sourceDBPass     string
	sourceDBHost     string
	sysUser          string // Plesk system user for the subscription
	sourceMageMode   string
	originalRedisPrefix string
	originalES6Prefix string
	originalES7Prefix string
	originalAmastyPrefix string
	sourceAdminFrontName string
	credsPath         string
}

// derive fills in derived/defaults after raw flags are parsed.
func (c *config) derive() error {
	if c.domain == "" {
		return fmt.Errorf("--domain is required")
	}
	if c.stagingName == "" {
		c.stagingName = "staging"
	}
	if c.sourcePath == "" {
		c.sourcePath = "/var/www/vhosts/" + c.domain + "/httpdocs"
	}
	if c.targetPath == "" {
		c.targetPath = "/var/www/vhosts/" + c.domain + "/" + c.stagingName
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
	if c.credsPath == "" {
		c.credsPath = "/var/www/vhosts/" + c.domain + "/.credentials/" + c.stagingName + ".json"
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
			colorGray, colorReset, fmt.Sprintf(format, args...))
	}
}

// infof prints always (informational progress).
func infof(format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, format, args...)
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

