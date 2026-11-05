package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// runMigrate handles the `migrate` subcommand: it runs on the DESTINATION
// Plesk server, pulls a live Magento site from a source Plesk server over
// SSH (key auth) and creates it as a staging copy in a NEW subscription
// named after --staging-domain.
func runMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	c := &config{}

	fs.StringVar(&c.remoteHost, "source-host", "", "Source (live) server IP or hostname")
	fs.StringVar(&c.remotePort, "ssh-port", "22", "SSH port of the source server")
	fs.StringVar(&c.remoteUser, "ssh-user", "root", "SSH user on the source server (must be able to run plesk db)")
	fs.StringVar(&c.sshKey, "ssh-key", defaultSSHKey, "SSH private key")
	fs.StringVar(&c.domain, "domain", "", "Live domain on the source server (e.g. example.com)")
	fs.StringVar(&c.stagingFQDN, "staging-domain", "", "Full host name of the new staging subscription (e.g. example.staging.example.net)")
	fs.StringVar(&c.sourcePath, "source-path", "", "Magento root on the source (default: from source Plesk document root)")
	fs.StringVar(&c.servicePlan, "service-plan", "Default Domain", "Plesk service plan for the new subscription")
	fs.StringVar(&c.subOwner, "owner", "admin", "Plesk owner (customer/reseller login) of the new subscription")
	fs.StringVar(&c.targetIP, "ip", "", "IP address for the new subscription (default: the only IPv4 in Plesk)")
	fs.StringVar(&c.subLogin, "sys-user", "", "System user of the new subscription (default: derived from --staging-domain)")
	fs.StringVar(&c.targetDB, "target-db", "", "Target database name")
	fs.StringVar(&c.targetDBUser, "target-db-user", "", "Target DB user")
	fs.StringVar(&c.targetDBPass, "target-db-pass", "", "Target DB password")
	fs.StringVar(&c.redisIDPrefix, "redis-id-prefix", "", "Redis cache id_prefix")
	fs.StringVar(&c.elasticSuffix, "elastic-suffix", "stg", "Elasticsearch index suffix")
	fs.StringVar(&c.basicAuthUser, "basic-auth-user", "admin", "HTTP Basic Auth user")
	fs.StringVar(&c.basicAuthPass, "basic-auth-pass", "", "HTTP Basic Auth password")
	fs.StringVar(&c.phpBin, "php-bin", "", "PHP CLI binary on THIS server")
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
	fs.BoolVar(&c.noSalesData, "no-sales-data", false, "Skip data for sales tables (schema only)")
	fs.BoolVar(&c.noCustomerData, "no-customer-data", false, "Skip data for customer tables (schema only)")
	fs.BoolVar(&c.dryRun, "dry-run", false, "Preview only, no changes on this server")
	fs.BoolVar(&c.nonInteractive, "non-interactive", false, "Fail if any required flag is missing")
	fs.BoolVar(&c.verbose, "verbose", false, "Verbose output")
	fs.StringVar(&c.logFile, "log-file", "", "Write a copy of all output to this file")
	_ = fs.Parse(args)

	globalConfig = c
	_ = c.derive()
	c.stagingName = "staging" // credentials file name: .credentials/staging.json
	c.stagingFQDN = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(c.stagingFQDN), "."))

	if os.Geteuid() != 0 {
		failf("this tool must be run as root (try: sudo %s)", strings.Join(os.Args, " "))
	}

	// Required inputs (prompted when interactive).
	need := func(val *string, flagName, question string, validate func(string) error) {
		if *val != "" {
			if err := validate(*val); err != nil {
				failf("--%s: %v", flagName, err)
			}
			return
		}
		if c.nonInteractive {
			failf("--%s is required in non-interactive mode", flagName)
		}
		*val = promptValue(question, "", validate)
	}
	need(&c.remoteHost, "source-host", "Source (live) server IP/hostname", validateNonEmpty)
	need(&c.domain, "domain", "Live domain on the source server", validateDomain)
	need(&c.stagingFQDN, "staging-domain", "New staging domain on this server", validateDomain)
	if _, err := safeDomainSQL(c.domain); err != nil {
		failf("--domain: %v", err)
	}
	if _, err := safeDomainSQL(c.stagingFQDN); err != nil {
		failf("--staging-domain: %v", err)
	}

	printBanner(version, commit, c.domain)

	// 1. Source side (read-only): SSH, Plesk lookup, env.php.
	sp := newSpinner(fmt.Sprintf("Connecting to %s (port %s, key %s)", remoteSpec(c), c.remotePort, c.sshKey))
	sp.Start()
	if err := checkSSH(c); err != nil {
		sp.Stop(false, err.Error())
		failf("%v", err)
	}
	sp.Stop(true, fmt.Sprintf("connected to %s", bold(c.remoteHost)))

	sp = newSpinner(fmt.Sprintf("Locating %s on the source via Plesk", c.domain))
	sp.Start()
	if err := resolveRemoteSource(c); err != nil {
		sp.Stop(false, err.Error())
		failf("%v", err)
	}
	sp.Stop(true, fmt.Sprintf("source: %s:%s", c.remoteHost, bold(c.sourcePath)))

	sp = newSpinner("Reading source env.php")
	sp.Start()
	if err := readRemoteEnvAndDerive(c); err != nil {
		sp.Stop(false, err.Error())
		failf("%v", err)
	}
	sp.Stop(true, fmt.Sprintf("env.php parsed (DB %s, PHP handler %s)", bold(c.sourceDB), bold(orDash(c.phpHandlerID))))

	// 2. Destination side (this server).
	c.targetDBHost = "localhost"
	c.webspaceName = c.stagingFQDN
	if c.subLogin == "" {
		c.subLogin = freeSystemUser(subscriptionLoginFor(c.stagingFQDN))
	} else if systemUserExists(c.subLogin) {
		failf("--sys-user %s already exists on this server", c.subLogin)
	}
	c.sysUser = c.subLogin
	c.subPass = strongPassword(20)
	if c.targetDBPass == "" {
		c.targetDBPass = generatePassword(24)
	}
	if c.basicAuthPass == "" && !c.skipBasicAuth {
		c.basicAuthPass = generatePassword(16)
	}
	if c.targetIP == "" {
		ips, err := pleskIPv4Addresses()
		switch {
		case err != nil:
			failf("could not list Plesk IP addresses: %v (pass --ip)", err)
		case len(ips) == 1:
			c.targetIP = ips[0]
		case c.nonInteractive || len(ips) == 0:
			failf("cannot choose an IP automatically (found: %s) — pass --ip", strings.Join(ips, ", "))
		default:
			printInfo("IPv4 addresses on this server: %s", strings.Join(ips, ", "))
			c.targetIP = promptValue("IP for the new subscription", ips[0], validateNonEmpty)
		}
	}
	c.webspaceRoot = vhostsRoot() + "/" + c.stagingFQDN
	c.targetPath = c.webspaceRoot + "/httpdocs"
	c.credsPath = c.webspaceRoot + "/.credentials/staging.json"

	// Local PHP CLI for bin/magento: same version as production.
	if c.phpBin == "" || c.phpBin == "/usr/bin/php" {
		c.phpBin = detectPHPBin(c, "/nonexistent")
	}

	// Disk estimate (read-only, measured on the source).
	printStep(0, 13, "Disk space estimate (read-only, on source)")
	est, estErr := estimateStagingSize(c)
	if estErr != nil {
		warnf("size estimate failed: %v", estErr)
		est = &sizeEstimate{}
	} else {
		printSizeEstimate(c, est)
		if avail := diskAvailable(vhostsRoot()); avail > 0 {
			need := est.StagingFilesBytes + est.StagingDBBytes
			if need > avail {
				warnf("staging needs ~%s but only %s are free on %s", humanSize(need), humanSize(avail), vhostsRoot())
			} else {
				printInfo("free on this server: %s", humanSize(avail))
			}
		}
	}

	if !c.nonInteractive {
		printMigrateSummary(c)
		if !promptConfirm("Proceed with migration?", false) {
			infof("aborted by user")
			os.Exit(0)
		}
	}

	fetchOriginalESPrefixes(c)
	if c.dryRun {
		printHeader("DRY RUN — no changes will be made on this server")
	}

	total := 13
	// rollback removes the subscription created by this run (which also
	// removes its files, database and DB user).
	rollback := func(err error) {
		if c.createdSub {
			if rerr := pleskSubscriptionRemove(c.stagingFQDN); rerr != nil {
				failf("%v\nrollback failed: %v", err, rerr)
			}
			failf("%v (rolled back: subscription %s removed)", err, c.stagingFQDN)
		}
		failf("%v", err)
	}

	printStep(1, total, "Pre-flight validation")
	if pleskSiteExists(c.stagingFQDN) {
		failf("%s already exists on this server (remove it first: magento-staging cleanup --domain %s)",
			c.stagingFQDN, c.stagingFQDN)
	}
	if pleskDatabaseExists(c) {
		failf("database %s already exists on this server (use --target-db)", c.targetDB)
	}
	printOK("no existing subscription / database")

	printStep(2, total, "Create Plesk subscription + database")
	sp = newSpinner(fmt.Sprintf("Creating subscription %s (plan %q, IP %s, user %s)",
		c.stagingFQDN, c.servicePlan, c.targetIP, c.subLogin))
	sp.Start()
	if err := pleskSubscriptionCreate(c); err != nil {
		sp.Stop(false, err.Error())
		failf("%v", err)
	}
	sp.Stop(true, fmt.Sprintf("subscription %s created", bold(c.stagingFQDN)))
	if !c.dryRun {
		// Use the real paths Plesk assigned.
		if docroot, err := pleskDocumentRoot(c.stagingFQDN); err == nil {
			if root := vhostAncestor(vhostsRoot(), docroot); root != "" {
				c.webspaceRoot = root
				c.targetPath = strings.TrimRight(docroot, "/")
				c.credsPath = root + "/.credentials/staging.json"
			}
		}
	}
	if c.phpHandlerID != "" {
		if err := pleskSiteUpdate(c, "-php", "true", "-php_handler_id", c.phpHandlerID); err != nil {
			warnf("could not set PHP handler %s (is it installed here?): %v", c.phpHandlerID, err)
		}
	}
	if err := pleskDatabaseCreate(c); err != nil {
		rollback(err)
	}
	if err := pleskDatabaseUserCreate(c); err != nil {
		rollback(err)
	}
	printOK("database %s created (user %s)", bold(c.targetDB), bold(c.targetDBUser))

	if c.logFile == "" {
		c.logFile = c.webspaceRoot + "/.credentials/staging.log"
	}
	if !c.dryRun {
		if err := c.openLog(); err != nil {
			warnf("could not open log file %s: %v", c.logFile, err)
		} else if c.logWriter != nil {
			defer c.closeLog()
			printInfo("Logging to %s", c.logFile)
		}
	}

	printStep(3, total, "Copy files from source (rsync over SSH)")
	if !c.dryRun {
		// Fresh subscription: drop Plesk's default index page etc.
		if out, err := run("find", c.targetPath, "-mindepth", "1", "-delete"); err != nil {
			rollback(fmt.Errorf("clearing %s failed: %v\n%s", c.targetPath, err, out))
		}
	}
	if err := rsyncCopy(c); err != nil {
		rollback(err)
	}

	printStep(4, total, "Point document root to pub/")
	if err := pleskSiteUpdate(c, "-www-root", relToWebspace(c, c.targetPath)+"/pub"); err != nil {
		rollback(err)
	}
	printOK("document root: %s/pub", c.targetPath)

	printStep(5, total, "Copy database from source (mysqldump over SSH)")
	schemaOnlyTables, err := getSchemaOnlyTables(c)
	if err != nil {
		warnf("could not determine schema-only tables: %v (continuing with full data)", err)
		schemaOnlyTables = nil
	}
	printInfo("%s%d%s tables will be schema-only (no data)", colorBold, len(schemaOnlyTables), colorReset)
	if err := mysqldumpPipeRemote(c, schemaOnlyTables); err != nil {
		rollback(err)
	}

	printStep(6, total, "Patch env.php (DB credentials + Redis prefix)")
	if err := patchTargetEnv(c); err != nil {
		failf("%v\nNOTE: subscription %s left in place; remove with: plesk bin subscription --remove %s",
			err, c.stagingFQDN, c.stagingFQDN)
	}
	printOK("env.php patched (DB=%s, Redis prefix=%s)", bold(c.targetDB), bold(c.redisIDPrefix))

	printStep(7, total, "Update core_config_data (URLs, SMTP, ES prefixes, payments)")
	if err := applyStagingConfigSQL(c); err != nil {
		failf("%v\nNOTE: subscription %s left in place", err, c.stagingFQDN)
	}
	printOK("core_config_data updated")

	printStep(8, total, "Magento CLI setup")
	// bin/magento runs as the subscription user: it must own the tree.
	if err := fixOwnership(c); err != nil {
		warnf("permission fix had errors: %v", err)
	}
	if err := magentoSetupUpgrade(c); err != nil {
		failf("%v\nNOTE: subscription %s left in place", err, c.stagingFQDN)
	}
	cleanMagentoGenerated(c)
	_ = magentoDICompile(c)
	_ = magentoDeployStaticContent(c)
	_ = magentoCacheFlush(c)
	_ = magentoReindex(c)
	_ = magentoDeployMode(c)
	_ = magentoMaintenanceDisable(c)

	printStep(9, total, "Fix ownership and permissions")
	if err := fixOwnership(c); err != nil {
		warnf("permission fix had errors: %v", err)
	}
	fixHtaccessFollowSymLinks(c)

	printStep(10, total, "Issue SSL certificate")
	if c.skipSSL {
		printInfo("skipped (--skip-ssl)")
	} else if err := pleskIssueSSL(c); err != nil {
		warnf("SSL issuance failed (DNS of %s must point to this server): %v", c.stagingFQDN, err)
	} else if !c.dryRun {
		printOK("Let's Encrypt certificate issued")
	}

	printStep(11, total, "Enable HTTP Basic Auth")
	if !c.skipBasicAuth {
		htpasswdPath := c.webspaceRoot + "/.htpasswd-staging"
		if err := writeHtpasswd(c, htpasswdPath); err != nil {
			warnf("htpasswd write failed: %v", err)
		}
		if err := pleskProtectedURLCreate(c, htpasswdPath); err != nil {
			warnf("protected URL setup failed: %v", err)
		}
	}

	printStep(12, total, "Verify staging")
	if err := verifyStagingDB(c); err != nil {
		warnf("DB verification: %v", err)
	}
	if err := verifyHTTP(c); err != nil {
		warnf("HTTP verification: %v", err)
	}

	printStep(13, total, "Save credentials")
	if err := saveCredentials(c); err != nil {
		warnf("failed to save credentials: %v", err)
	}

	printFinalSummary(c, schemaOnlyTables, est)
	printKeyValue("Subscription", fmt.Sprintf("%s (system user %s)", c.stagingFQDN, c.subLogin))
	printKeyValue("Source", fmt.Sprintf("%s:%s", c.remoteHost, c.sourcePath))
}

// relToWebspace returns p relative to the webspace root (Plesk -www-root).
func relToWebspace(c *config, p string) string {
	return strings.TrimPrefix(strings.TrimPrefix(p, c.webspaceRoot), "/")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// printMigrateSummary prints the plan before execution.
func printMigrateSummary(c *config) {
	printHeader("Migration plan")
	printKeyValue("Source server", fmt.Sprintf("%s (port %s, key %s)", remoteSpec(c), c.remotePort, c.sshKey))
	printKeyValue("Source", fmt.Sprintf("%s -> %s", c.domain, c.sourcePath))
	printKeyValue("Source DB", c.sourceDB)
	printKeyValue("New subscription", fmt.Sprintf("%s (plan %q, owner %s, IP %s)", c.stagingFQDN, c.servicePlan, c.subOwner, c.targetIP))
	printKeyValue("System user", c.subLogin)
	printKeyValue("Target", c.targetPath+" (docroot pub/)")
	printKeyValue("Staging URL", c.stagingURL())
	printKeyValue("Target DB", fmt.Sprintf("%s (user: %s)", c.targetDB, c.targetDBUser))
	printKeyValue("Redis prefix", c.redisIDPrefix)
	printKeyValue("PHP", fmt.Sprintf("handler %s, CLI %s", orDash(c.phpHandlerID), c.phpBin))
	fmt.Fprintln(os.Stderr)
}
