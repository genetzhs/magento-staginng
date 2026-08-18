package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// runCreate handles the `create` subcommand.
func runCreate(args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	c := &config{}

	fs.StringVar(&c.domain, "domain", "", "Target domain (e.g. example.com)")
	fs.StringVar(&c.stagingName, "staging-name", "staging", "Staging subdomain prefix")
	fs.StringVar(&c.sourcePath, "source-path", "", "Source httpdocs path")
	fs.StringVar(&c.targetPath, "target-path", "", "Target staging path")
	fs.StringVar(&c.sourceDB, "source-db", "", "Source database (auto-detected)")
	fs.StringVar(&c.targetDB, "target-db", "", "Target database name")
	fs.StringVar(&c.targetDBUser, "target-db-user", "", "Target DB user")
	fs.StringVar(&c.targetDBPass, "target-db-pass", "", "Target DB password")
	fs.StringVar(&c.redisIDPrefix, "redis-id-prefix", "", "Redis cache id_prefix")
	fs.StringVar(&c.elasticSuffix, "elastic-suffix", "stg", "Elasticsearch index suffix")
	fs.StringVar(&c.basicAuthUser, "basic-auth-user", "admin", "HTTP Basic Auth user")
	fs.StringVar(&c.basicAuthPass, "basic-auth-pass", "", "HTTP Basic Auth password")
	fs.StringVar(&c.phpBin, "php-bin", "", "PHP CLI binary path")
	fs.StringVar(&c.magentoMode, "magento-mode", "", "Set MAGE_MODE (developer|production)")
	fs.StringVar(&c.schemaOnlyFile, "schema-only-file", "", "File with table names to copy schema-only")
	fs.BoolVar(&c.noSchemaOnly, "no-schema-only", false, "Copy full data for all tables")
	fs.BoolVar(&c.includeGit, "include-git", false, "Include .git/ in rsync")
	fs.BoolVar(&c.skipSSL, "skip-ssl", false, "Skip Let's Encrypt issuance")
	fs.BoolVar(&c.skipBasicAuth, "skip-basic-auth", false, "Skip HTTP Basic Auth")
	fs.BoolVar(&c.skipPaymentDisable, "skip-payment-disable", false, "Do not disable payment methods")
	fs.BoolVar(&c.skipAnalyticsDisable, "skip-analytics-disable", false, "Do not disable analytics")
	fs.BoolVar(&c.skipCronDisable, "skip-cron-disable", false, "Do not set dev/cron/disable=1")
	fs.BoolVar(&c.skipEmailDisable, "skip-email-disable", false, "Do not disable outgoing emails")
	fs.BoolVar(&c.skipSEODisable, "skip-seo-disable", false, "Do not set NOINDEX,NOFOLLOW")
	fs.BoolVar(&c.noSalesData, "no-sales-data", false, "Skip data for sales/quote/invoice/shipment tables (schema only)")
	fs.BoolVar(&c.noCustomerData, "no-customer-data", false, "Skip data for customer/newsletter/review/wishlist tables (schema only)")
	fs.BoolVar(&c.dryRun, "dry-run", false, "Preview only, no changes")
	fs.BoolVar(&c.nonInteractive, "non-interactive", false, "Fail if any required flag is missing")
	fs.BoolVar(&c.verbose, "verbose", false, "Verbose output")

	_ = fs.Parse(args)

	// Handle --version / --help edge cases
	for _, a := range args {
		if a == "--version" {
			showVersion()
		}
		if a == "--check-update" {
			checkGitHubUpdate()
			return
		}
	}

	if err := c.derive(); err != nil {
		failf("%v", err)
	}

	// Must be root
	if os.Geteuid() != 0 {
		failf("this tool must be run as root (try: sudo %s)", strings.Join(os.Args, " "))
	}

	infof("magento-staging %s — creating staging for %s", version, c.domain)

	// Resolve domain interactively if missing
	if c.domain == "" {
		if c.nonInteractive {
			failf("--domain is required in non-interactive mode")
		}
		domains, _ := pleskListDomains()
		if len(domains) > 0 {
			infof("Available domains on this server:")
			for i, d := range domains {
				infof("  %d. %s", i+1, d)
			}
		}
		c.domain = promptValue("Domain to stage", "", validateDomain)
		// re-derive paths now that domain is known
		if err := c.derive(); err != nil {
			failf("%v", err)
		}
	}

	// Resolve system user
	su, err := pleskSiteInfo(c.domain)
	if err != nil {
		failf("could not resolve system user for %s: %v", c.domain, err)
	}
	c.sysUser = su
	infof("system user: %s", c.sysUser)

	// Verify source httpdocs exists
	if !pathExists(c.sourcePath) {
		failf("source path does not exist: %s", c.sourcePath)
	}
	if !pathExists(c.sourcePath + "/app/etc/env.php") {
		failf("source is not a Magento installation (no app/etc/env.php in %s)", c.sourcePath)
	}

	// Load source env.php to derive DB name/user/pass etc.
	infof("reading source env.php...")
	if err := readSourceEnvAndDerive(c); err != nil {
		failf("%v", err)
	}

	// Generate credentials if not provided
	if c.targetDBPass == "" {
		c.targetDBPass = generatePassword(24)
	}
	if c.basicAuthPass == "" && !c.skipBasicAuth {
		c.basicAuthPass = generatePassword(16)
	}

	// Disk space estimate (read-only; runs before any change).
	// Useful for confirming the staging will fit and showing the user what
	// will be skipped.
	stepf(0, 12, "Disk space estimate (read-only)")
	est, estErr := estimateStagingSize(c)
	if estErr != nil {
		warnf("size estimate failed: %v", estErr)
		est = &sizeEstimate{}
	} else {
		printSizeEstimate(c, est)
	}

	// Interactive confirmation
	if !c.nonInteractive {
		printCreateSummary(c)
		if !promptConfirm("Proceed with staging creation?", false) {
			infof("aborted by user")
			os.Exit(0)
		}
	}

	// Fetch original ES prefixes for reporting
	fetchOriginalESPrefixes(c)

	if c.dryRun {
		infof("\n=== DRY RUN — no changes will be made ===")
	}

	// Run phases
	total := 12
	stepf(1, total, "Pre-flight validation")
	if err := preflightChecks(c); err != nil {
		failf("%v", err)
	}

	stepf(2, total, "Create Plesk subdomain + database")
	if err := pleskSubdomainCreate(c); err != nil {
		failf("%v", err)
	}
	if err := pleskDatabaseCreate(c); err != nil {
		_ = pleskSubdomainRemove(c)
		failf("%v (rolled back subdomain)", err)
	}
	if err := pleskDatabaseUserCreate(c); err != nil {
		_ = pleskDatabaseRemove(c)
		_ = pleskSubdomainRemove(c)
		failf("%v (rolled back DB + subdomain)", err)
	}

	stepf(3, total, "Issue SSL certificate")
	if !c.skipSSL {
		if err := pleskIssueSSL(c); err != nil {
			warnf("SSL issuance failed: %v", err)
			warnf("you can retry later with: plesk bin extension --exec letsencrypt le.php -d %s -d %s.%s",
				c.domain, c.stagingName, c.domain)
			infof("continuing with self-signed cert (HTTP will still work)")
		}
	} else {
		infof("  skipped (--skip-ssl)")
	}

	stepf(4, total, "Copy files (rsync with excludes)")
	if err := rsyncCopy(c); err != nil {
		// Rollback
		_ = pleskDatabaseUserRemove(c)
		_ = pleskDatabaseRemove(c)
		_ = pleskSubdomainRemove(c)
		failf("%v (rolled back Plesk resources)", err)
	}

	stepf(5, total, "Copy database (mysqldump pipe with schema-only tables)")
	schemaOnlyTables, err := getSchemaOnlyTables(c)
	if err != nil {
		warnf("could not determine schema-only tables: %v (continuing with full data)", err)
		schemaOnlyTables = nil
	}
	infof("  %d tables will be schema-only (no data)", len(schemaOnlyTables))
	if err := mysqldumpPipe(c, schemaOnlyTables); err != nil {
		_ = pleskDatabaseUserRemove(c)
		_ = pleskDatabaseRemove(c)
		_ = pleskSubdomainRemove(c)
		_ = removeTargetHttpdocs(c)
		failf("%v (rolled back)", err)
	}

	stepf(6, total, "Patch env.php (DB credentials + Redis prefix)")
	if err := patchTargetEnv(c); err != nil {
		failf("%v\nNOTE: Plesk resources created; manual cleanup needed:\n  plesk bin subdomain --remove %s.%s\n  plesk bin database --remove %s",
			err, c.stagingName, c.domain, c.targetDB)
	}

	stepf(7, total, "Update core_config_data (URLs, SMTP, ES prefixes, payments)")
	if err := applyStagingConfigSQL(c); err != nil {
		failf("%v\nNOTE: Plesk resources + files created; manual cleanup needed", err)
	}

	// Phases 8+ are NOT auto-rolled back (per user choice)
	stepf(8, total, "Magento CLI setup (setup:upgrade / cache:flush / reindex)")
	if err := magentoSetupUpgrade(c); err != nil {
		failf("%v\nNOTE: Plesk resources + DB prepared; manual cleanup:", err)
	}
	_ = magentoCacheFlush(c)
	_ = magentoReindex(c)
	_ = magentoDeployMode(c)
	_ = magentoMaintenanceDisable(c)

	stepf(9, total, "Fix ownership and permissions")
	if err := fixOwnership(c); err != nil {
		warnf("permission fix had errors: %v", err)
	}

	stepf(10, total, "Enable HTTP Basic Auth")
	if !c.skipBasicAuth {
		htpasswdPath := c.targetPath + "/.htpasswd"
		if err := writeHtpasswd(c, htpasswdPath); err != nil {
			warnf("htpasswd write failed: %v", err)
		}
		if err := pleskProtectedURLCreate(c, htpasswdPath); err != nil {
			warnf("protected URL setup failed (basic auth may not be active): %v", err)
		}
	}

	stepf(11, total, "Verify staging")
	if err := verifyStagingDB(c); err != nil {
		warnf("DB verification: %v", err)
	}
	if err := verifyHTTP(c); err != nil {
		warnf("HTTP verification: %v", err)
	}

	stepf(12, total, "Save credentials")
	if err := saveCredentials(c); err != nil {
		warnf("failed to save credentials: %v", err)
	}

	// Final summary
	printFinalSummary(c, schemaOnlyTables, est)
}

// preflightChecks verifies that staging resources do not already exist.
func preflightChecks(c *config) error {
	if pleskSubdomainExists(c) {
		return fmt.Errorf("subdomain %s.%s already exists (cleanup with: magento-staging cleanup --domain %s --staging-name %s)",
			c.stagingName, c.domain, c.domain, c.stagingName)
	}
	if pleskDatabaseExists(c) {
		return fmt.Errorf("database %s already exists (drop it first or use a different --target-db)", c.targetDB)
	}
	if pathExists(c.targetPath) && !pathIsEmpty(c.targetPath) {
		return fmt.Errorf("target path %s already exists and is not empty", c.targetPath)
	}
	return nil
}

// patchTargetEnv reads the freshly-rsynced env.php from the staging path,
// patches it with staging values, and writes it back.
func patchTargetEnv(c *config) error {
	envPath := c.targetPath + "/httpdocs/app/etc/env.php"
	if c.dryRun {
		infof("  [dry-run] patch %s (DB creds, Redis prefix %s)", envPath, c.redisIDPrefix)
		return nil
	}
	env, err := loadEnvPHP(c.phpBin, envPath)
	if err != nil {
		return fmt.Errorf("load target env.php: %v", err)
	}
	env = patchEnv(env, c)
	if err := writeEnvPHP(c.phpBin, envPath, env); err != nil {
		return fmt.Errorf("write target env.php: %v", err)
	}
	// Ensure ownership (env.php was just written by root)
	if out, err := run("chown", c.sysUser+":psaserv", envPath); err != nil {
		warnf("chown env.php: %v\n%s", err, out)
	}
	_ = os.Chmod(envPath, 0640)
	return nil
}

// verifyHTTP checks that the staging URL responds.
func verifyHTTP(c *config) error {
	if c.dryRun {
		infof("  [dry-run] curl %s", c.stagingURL())
		return nil
	}
	url := c.stagingURL()
	infof("  curl %s", url)
	out, err := run("curl", "-skI", "-o", "/dev/null", "-w", "%{http_code}", url)
	if err != nil {
		return fmt.Errorf("curl failed: %v", err)
	}
	code := strings.TrimSpace(out)
	if code == "401" {
		infof("  ✓ HTTP 401 (basic auth active)")
		return nil
	}
	if code == "200" || code == "302" {
		if c.skipBasicAuth {
			infof("  ✓ HTTP %s (no auth)", code)
			return nil
		}
		warnf("  got HTTP %s but basic auth expected 401 (auth may not be active yet)", code)
		return nil
	}
	return fmt.Errorf("unexpected HTTP status: %s", code)
}

// removeTargetHttpdocs removes the staging target directory contents.
func removeTargetHttpdocs(c *config) error {
	out, err := run("rm", "-rf", c.targetPath)
	if err != nil {
		return fmt.Errorf("rm failed: %v\n%s", err, out)
	}
	return nil
}

// pleskDatabaseUserRemove removes the target DB user.
func pleskDatabaseUserRemove(c *config) error {
	args := []string{"/usr/sbin/plesk", "bin", "database", "--remove-dbuser", c.targetDBUser}
	out, err := run(args[0], args[1:]...)
	if err != nil {
		return fmt.Errorf("remove db user failed: %v\n%s", err, out)
	}
	return nil
}

// printCreateSummary prints the plan before execution (interactive confirmation).
func printCreateSummary(c *config) {
	infof("\n=== Staging creation plan ===")
	infof("  Source:           %s", c.sourcePath)
	infof("  Target:           %s", c.targetPath)
	infof("  Staging URL:      %s", c.stagingURL())
	infof("  Source DB:        %s", c.sourceDB)
	infof("  Target DB:        %s (user: %s)", c.targetDB, c.targetDBUser)
	infof("  Redis prefix:     %s", c.redisIDPrefix)
	infof("  ES suffix:        %s", c.elasticSuffix)
	infof("  System user:      %s", c.sysUser)
	infof("  PHP binary:       %s", c.phpBin)
	if !c.skipBasicAuth {
		infof("  Basic Auth:       %s (password will be generated)", c.basicAuthUser)
	}
	infof("  Schema-only:      %d base table patterns", len(schemaOnlyPatterns))
	if c.noSalesData {
		infof("  + sales data skipped: orders/invoices/shipments/quotes/rules (%d patterns)", len(salesSchemaOnlyPatterns))
	}
	if c.noCustomerData {
		infof("  + customer data skipped: customers/newsletter/reviews/wishlist (%d patterns)", len(customerSchemaOnlyPatterns))
	}
	infof("")
}

// printFinalSummary prints the result summary at the end.
func printFinalSummary(c *config, schemaOnlyTables []string, est *sizeEstimate) {
	infof("\n" + strings.Repeat("=", 60))
	infof(" ✓ Staging created successfully")
	infof(strings.Repeat("=", 60))
	infof("  Staging URL:    %s", c.stagingURL())
	if c.sourceAdminFrontName != "" {
		infof("  Admin panel:    %s%s", c.stagingURL(), c.sourceAdminFrontName)
	}
	if !c.skipBasicAuth {
		infof("  Basic auth:     %s / %s", c.basicAuthUser, c.basicAuthPass)
	}
	infof("  Path:           %s/httpdocs", c.targetPath)
	infof("  Database:       %s", c.targetDB)
	infof("  DB user:        %s", c.targetDBUser)
	infof("  Redis prefix:   %s", c.redisIDPrefix)
	if c.originalES6Prefix != "" || c.originalES7Prefix != "" || c.originalAmastyPrefix != "" {
		infof("  ES prefixes:")
		if c.originalES6Prefix != "" {
			infof("    ES6:    %s -> %s%s", c.originalES6Prefix, c.originalES6Prefix, c.elasticSuffix)
		}
		if c.originalES7Prefix != "" {
			infof("    ES7:    %s -> %s%s", c.originalES7Prefix, c.originalES7Prefix, c.elasticSuffix)
		}
		if c.originalAmastyPrefix != "" {
			infof("    Amasty: %s -> %s%s", c.originalAmastyPrefix, c.originalAmastyPrefix, c.elasticSuffix)
		}
	}
	infof("  Schema-only:    %d tables (data skipped)", len(schemaOnlyTables))
	if c.noSalesData {
		infof("    (includes sales: orders/invoices/shipments/quotes/rules)")
	}
	if c.noCustomerData {
		infof("    (includes customers/newsletter/reviews/wishlist — GDPR-safe)")
	}
	if est != nil {
		infof("  Files size:     %s source -> %s staging (%s saved)",
			humanSize(est.SourceFilesBytes), humanSize(est.StagingFilesBytes),
			humanSize(est.FilesExcludedBytes))
		infof("  DB size:        %s source -> %s staging (%s skipped)",
			humanSize(est.SourceDBBytes), humanSize(est.StagingDBBytes),
			humanSize(est.DBSchemaOnlyBytes))
		infof("  Total staging:  %s", humanSize(est.StagingFilesBytes+est.StagingDBBytes))
	}
	infof("  Credentials:    %s", c.credsPath)
	infof("")
	infof("  Safeguards applied:")
	if !c.skipEmailDisable {
		infof("    ✓ Outgoing emails disabled (system/smtp/disable=1)")
	}
	if !c.skipSEODisable {
		infof("    ✓ NOINDEX,NOFOLLOW")
	}
	if !c.skipCronDisable {
		infof("    ✓ Cron disabled (dev/cron/disable=1)")
	}
	if !c.skipPaymentDisable {
		infof("    ✓ Payments disabled")
	}
	if !c.skipAnalyticsDisable {
		infof("    ✓ Analytics disabled")
	}
	if !c.skipBasicAuth {
		infof("    ✓ HTTP Basic Auth enabled")
	}
	if !c.skipSSL {
		infof("    ✓ SSL (Let's Encrypt)")
	}
	infof(strings.Repeat("=", 60))
}
