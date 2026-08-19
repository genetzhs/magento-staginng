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
	fs.StringVar(&c.logFile, "log-file", "", "Write a copy of all output to this file")

	_ = fs.Parse(args)

	// Wire global config so package-level infof/warnf can log too.
	globalConfig = c
	if c.logFile == "" {
		// Default log location: alongside credentials
		c.logFile = "/var/www/vhosts/" + c.domain + "/.credentials/" + c.stagingName + ".log"
	}
	if err := c.openLog(); err != nil {
		// Don't fail — log is best-effort
		warnf("could not open log file %s: %v", c.logFile, err)
	} else if c.logWriter != nil {
		defer c.closeLog()
		printInfo("Logging to %s", c.logFile)
	}

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

	printBanner(version, commit, c.domain)

	// Resolve domain interactively if missing
	if c.domain == "" {
		if c.nonInteractive {
			failf("--domain is required in non-interactive mode")
		}
		domains, _ := pleskListDomains()
		if len(domains) > 0 {
			printHeader("Available domains on this server")
			for i, d := range domains {
				printInfo("%s%d.%s %s", colorWhite, i+1, colorReset, d)
			}
		}
		c.domain = promptValue("Domain to stage", "", validateDomain)
		// re-derive paths now that domain is known
		if err := c.derive(); err != nil {
			failf("%v", err)
		}
	}

	// Resolve system user
	spinner := newSpinner(fmt.Sprintf("Resolving system user for %s", c.domain))
	spinner.Start()
	su, err := pleskSiteInfo(c.domain)
	if err != nil {
		spinner.Stop(false, fmt.Sprintf("could not resolve system user: %v", err))
		failf("could not resolve system user for %s: %v", c.domain, err)
	}
	c.sysUser = su
	spinner.Stop(true, fmt.Sprintf("system user: %s", bold(c.sysUser)))

	// Verify source httpdocs exists
	if !pathExists(c.sourcePath) {
		failf("source path does not exist: %s", c.sourcePath)
	}
	if !pathExists(c.sourcePath + "/app/etc/env.php") {
		failf("source is not a Magento installation (no app/etc/env.php in %s)", c.sourcePath)
	}

	// Load source env.php to derive DB name/user/pass etc.
	spinner = newSpinner("Reading source env.php")
	spinner.Start()
	if err := readSourceEnvAndDerive(c); err != nil {
		spinner.Stop(false, fmt.Sprintf("%v", err))
		failf("%v", err)
	}
	spinner.Stop(true, fmt.Sprintf("env.php parsed (PHP %s, DB %s)",
		bold(c.phpBin), bold(c.sourceDB)))

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
		printHeader("DRY RUN — no changes will be made")
	}

	// Run phases
	total := 12

	// Phase 1: pre-flight
	printStep(1, total, "Pre-flight validation")
	sp := newSpinner("Checking for existing staging resources")
	sp.Start()
	if err := preflightChecks(c); err != nil {
		sp.Stop(false, fmt.Sprintf("%v", err))
		failf("%v", err)
	}
	sp.Stop(true, "no existing staging resources found")

	// Phase 2: Plesk resources
	printStep(2, total, "Create Plesk subdomain + database")
	sp = newSpinner(fmt.Sprintf("Creating subdomain %s.%s", c.stagingName, c.domain))
	sp.Start()
	if err := pleskSubdomainCreate(c); err != nil {
		sp.Stop(false, fmt.Sprintf("%v", err))
		failf("%v", err)
	}
	sp.Stop(true, fmt.Sprintf("subdomain %s created", bold(c.stagingName+"."+c.domain)))

	sp = newSpinner(fmt.Sprintf("Creating database %s", c.targetDB))
	sp.Start()
	if err := pleskDatabaseCreate(c); err != nil {
		sp.Stop(false, fmt.Sprintf("%v", err))
		_ = pleskSubdomainRemove(c)
		failf("%v (rolled back subdomain)", err)
	}
	if err := pleskDatabaseUserCreate(c); err != nil {
		sp.Stop(false, fmt.Sprintf("%v", err))
		_ = pleskDatabaseRemove(c)
		_ = pleskSubdomainRemove(c)
		failf("%v (rolled back DB + subdomain)", err)
	}
	sp.Stop(true, fmt.Sprintf("database %s created (user %s)",
		bold(c.targetDB), bold(c.targetDBUser)))

	// Phase 3: SSL
	printStep(3, total, "Issue SSL certificate")
	if !c.skipSSL {
		sp = newSpinner(fmt.Sprintf("Requesting Let's Encrypt cert for %s.%s", c.stagingName, c.domain))
		sp.Start()
		if err := pleskIssueSSL(c); err != nil {
			sp.Stop(false, fmt.Sprintf("SSL issuance failed: %v", err))
			warnf("you can retry later with: plesk bin extension --exec letsencrypt cli.php -d %s.%s",
				c.stagingName, c.domain)
			printInfo("continuing with self-signed cert (HTTPS will still work)")
		} else {
			sp.Stop(true, "Let's Encrypt certificate issued")
		}
	} else {
		printInfo("skipped (--skip-ssl)")
	}

	// Phase 4: rsync
	printStep(4, total, "Copy files (rsync with excludes)")
	if err := rsyncCopy(c); err != nil {
		_ = pleskDatabaseUserRemove(c)
		_ = pleskDatabaseRemove(c)
		_ = pleskSubdomainRemove(c)
		failf("%v (rolled back Plesk resources)", err)
	}

	// Phase 5: DB
	printStep(5, total, "Copy database (mysqldump pipe with schema-only tables)")
	schemaOnlyTables, err := getSchemaOnlyTables(c)
	if err != nil {
		warnf("could not determine schema-only tables: %v (continuing with full data)", err)
		schemaOnlyTables = nil
	}
	printInfo("%s%d%s tables will be schema-only (no data)",
		colorBold, len(schemaOnlyTables), colorReset)
	if err := mysqldumpPipe(c, schemaOnlyTables); err != nil {
		_ = pleskDatabaseUserRemove(c)
		_ = pleskDatabaseRemove(c)
		_ = pleskSubdomainRemove(c)
		_ = removeTargetHttpdocs(c)
		failf("%v (rolled back)", err)
	}

	// Phase 6: env.php
	printStep(6, total, "Patch env.php (DB credentials + Redis prefix)")
	if err := patchTargetEnv(c); err != nil {
		failf("%v\nNOTE: Plesk resources created; manual cleanup needed:\n  plesk bin subdomain --remove %s.%s\n  plesk bin database --remove %s",
			err, c.stagingName, c.domain, c.targetDB)
	}
	printOK("env.php patched (DB=%s, Redis prefix=%s)",
		bold(c.targetDB), bold(c.redisIDPrefix))

	// Phase 7: SQL updates
	printStep(7, total, "Update core_config_data (URLs, SMTP, ES prefixes, payments)")
	if err := applyStagingConfigSQL(c); err != nil {
		failf("%v\nNOTE: Plesk resources + files created; manual cleanup needed", err)
	}
	printOK("core_config_data updated (URLs, SMTP, SEO, ES, payments, analytics)")

	// Phase 8: Magento CLI (no auto-rollback)
	printStep(8, total, "Magento CLI setup (upgrade / di:compile / static-content / cache:flush)")
	// Per Adobe docs, after a URL change the following must run:
	//   1. setup:upgrade           (schema + data upgrades)
	//   2. setup:di:compile        (rebuild generated/code/ — production mode)
	//   3. setup:static-content:deploy  (CSS/JS/fonts to pub/static/)
	//   4. cache:flush             (clear var/cache, var/page_cache)
	//   5. indexer:reindex         (refresh search index for new URLs)
	//   6. deploy:mode:set         (if --magento-mode specified)
	//   7. maintenance:disable     (open the storefront)
	if err := magentoSetupUpgrade(c); err != nil {
		failf("%v\nNOTE: Plesk resources + DB prepared; manual cleanup:", err)
	}
	// Clean generated code & caches before DI compile / static deploy
	cleanMagentoGenerated(c)
	_ = magentoDICompile(c)
	_ = magentoDeployStaticContent(c)
	_ = magentoCacheFlush(c)
	_ = magentoReindex(c)
	_ = magentoDeployMode(c)
	_ = magentoMaintenanceDisable(c)

	// Phase 9: permissions
	printStep(9, total, "Fix ownership and permissions")
	if err := fixOwnership(c); err != nil {
		warnf("permission fix had errors: %v", err)
	}

	// Fix Plesk-incompatible .htaccess directives
	// Plesk's Apache config doesn't allow FollowSymLinks in .htaccess.
	// Replace with SymLinksIfOwnerMatch (more secure, Plesk-compatible).
	fixHtaccessFollowSymLinks(c)

	// Phase 10: basic auth
	printStep(10, total, "Enable HTTP Basic Auth")
	if !c.skipBasicAuth {
		htpasswdPath := c.targetPath + "/.htpasswd"
		if err := writeHtpasswd(c, htpasswdPath); err != nil {
			warnf("htpasswd write failed: %v", err)
		}
		if err := pleskProtectedURLCreate(c, htpasswdPath); err != nil {
			warnf("protected URL setup failed (basic auth may not be active): %v", err)
		}
	}

	// Phase 11: verify
	printStep(11, total, "Verify staging")
	if err := verifyStagingDB(c); err != nil {
		warnf("DB verification: %v", err)
	}
	if err := verifyHTTP(c); err != nil {
		warnf("HTTP verification: %v", err)
	}

	// Phase 12: save credentials
	printStep(12, total, "Save credentials")
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
	envPath := c.targetPath + "/app/etc/env.php"
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
		printInfo("[dry-run] curl %s", c.stagingURL())
		return nil
	}
	url := c.stagingURL()
	printInfo("curl %s", url)
	out, err := run("curl", "-skI", "-o", "/dev/null", "-w", "%{http_code}", url)
	if err != nil {
		return fmt.Errorf("curl failed: %v", err)
	}
	code := strings.TrimSpace(out)
	if code == "401" {
		printOK("HTTP 401 (basic auth active)")
		return nil
	}
	if code == "200" || code == "302" {
		if c.skipBasicAuth {
			printOK("HTTP %s (no auth)", code)
			return nil
		}
		warnf("got HTTP %s but basic auth expected 401 (auth may not be active yet)", code)
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
	printHeader("Staging creation plan")
	printKeyValue("Source", c.sourcePath)
	printKeyValue("Target", c.targetPath)
	printKeyValue("Staging URL", c.stagingURL())
	printKeyValue("Source DB", c.sourceDB)
	printKeyValue("Target DB", fmt.Sprintf("%s (user: %s)", c.targetDB, c.targetDBUser))
	printKeyValue("Redis prefix", c.redisIDPrefix)
	printKeyValue("ES suffix", c.elasticSuffix)
	printKeyValue("System user", c.sysUser)
	printKeyValue("PHP binary", c.phpBin)
	if !c.skipBasicAuth {
		printKeyValue("Basic Auth", fmt.Sprintf("%s (password will be generated)", c.basicAuthUser))
	}
	printKeyValue("Schema-only", fmt.Sprintf("%d base table patterns", len(schemaOnlyPatterns)))
	if c.noSalesData {
		printInfo("%s+ sales data skipped:%s orders/invoices/shipments/quotes/rules (%d patterns)",
			colorYellow, colorReset, len(salesSchemaOnlyPatterns))
	}
	if c.noCustomerData {
		printInfo("%s+ customer data skipped:%s customers/newsletter/reviews/wishlist (%d patterns)",
			colorYellow, colorReset, len(customerSchemaOnlyPatterns))
	}
	fmt.Fprintln(os.Stderr)
}

// printFinalSummary prints the result summary at the end.
func printFinalSummary(c *config, schemaOnlyTables []string, est *sizeEstimate) {
	printFinalBanner(true)
	printKeyValue("Staging URL", c.stagingURL())
	if c.sourceAdminFrontName != "" {
		printKeyValue("Admin panel", c.stagingURL()+c.sourceAdminFrontName)
	}
	if !c.skipBasicAuth {
		printKeyValue("Basic auth", fmt.Sprintf("%s / %s", c.basicAuthUser, c.basicAuthPass))
	}
	printKeyValue("Path", c.targetPath)
	printKeyValue("Database", c.targetDB)
	printKeyValue("DB user", c.targetDBUser)
	printKeyValue("Redis prefix", c.redisIDPrefix)
	if c.originalES6Prefix != "" || c.originalES7Prefix != "" || c.originalAmastyPrefix != "" {
		fmt.Fprintf(os.Stderr, "  %sES prefixes:%s\n", colorWhite, colorReset)
		if c.originalES6Prefix != "" {
			printInfo("ES6:    %s -> %s%s",
				c.originalES6Prefix, green(c.originalES6Prefix+c.elasticSuffix), colorReset)
		}
		if c.originalES7Prefix != "" {
			printInfo("ES7:    %s -> %s%s",
				c.originalES7Prefix, green(c.originalES7Prefix+c.elasticSuffix), colorReset)
		}
		if c.originalAmastyPrefix != "" {
			printInfo("Amasty: %s -> %s%s",
				c.originalAmastyPrefix, green(c.originalAmastyPrefix+c.elasticSuffix), colorReset)
		}
	}
	printKeyValue("Schema-only", fmt.Sprintf("%d tables (data skipped)", len(schemaOnlyTables)))
	if c.noSalesData {
		printInfo("(includes sales: orders/invoices/shipments/quotes/rules)")
	}
	if c.noCustomerData {
		printInfo("(includes customers/newsletter/reviews/wishlist — GDPR-safe)")
	}
	if est != nil {
		printKeyValue("Files size", fmt.Sprintf("%s source -> %s staging (%s saved)",
			humanSize(est.SourceFilesBytes), humanSize(est.StagingFilesBytes),
			humanSize(est.FilesExcludedBytes)))
		printKeyValue("DB size", fmt.Sprintf("%s source -> %s staging (%s skipped)",
			humanSize(est.SourceDBBytes), humanSize(est.StagingDBBytes),
			humanSize(est.DBSchemaOnlyBytes)))
		printKeyValue("Total staging", humanSize(est.StagingFilesBytes+est.StagingDBBytes))
	}
	printKeyValue("Credentials", c.credsPath)
	fmt.Fprintln(os.Stderr)

	fmt.Fprintf(os.Stderr, "  %sSafeguards applied:%s\n", colorBold, colorReset)
	if !c.skipEmailDisable {
		printOK("Outgoing emails disabled (system/smtp/disable=1)")
	}
	if !c.skipSEODisable {
		printOK("NOINDEX,NOFOLLOW")
	}
	if !c.skipCronDisable {
		printOK("Cron disabled (dev/cron/disable=1)")
	}
	if !c.skipPaymentDisable {
		printOK("Payments disabled")
	}
	if !c.skipAnalyticsDisable {
		printOK("Analytics disabled")
	}
	printOK("Cookie domain set to staging subdomain")
	if !c.skipBasicAuth {
		printOK("HTTP Basic Auth enabled")
	}
	if !c.skipSSL {
		printOK("SSL (Let's Encrypt)")
	}
	fmt.Fprintf(os.Stderr, "%s%s%s\n", colorGreen, strings.Repeat("═", 60), colorReset)
}
