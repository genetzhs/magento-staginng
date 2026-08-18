package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// getSchemaOnlyTables queries information_schema for tables whose name matches
// any of the schemaOnlyPatterns (using SQL LIKE matching) and returns the
// concrete list of tables in the source database that will be schema-only.
func getSchemaOnlyTables(c *config) ([]string, error) {
	if c.noSchemaOnly {
		return nil, nil
	}

	// If user provided a file, read it directly.
	if c.schemaOnlyFile != "" {
		data, err := os.ReadFile(c.schemaOnlyFile)
		if err != nil {
			return nil, fmt.Errorf("reading schema-only file: %v", err)
		}
		var tables []string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				tables = append(tables, line)
			}
		}
		return tables, nil
	}

	// Start with the default schema-only patterns.
	patterns := append([]string{}, schemaOnlyPatterns...)

	// Add sales patterns if --no-sales-data.
	if c.noSalesData {
		patterns = append(patterns, salesSchemaOnlyPatterns...)
	}
	// Add customer patterns if --no-customer-data.
	if c.noCustomerData {
		patterns = append(patterns, customerSchemaOnlyPatterns...)
	}

	if len(patterns) == 0 {
		return nil, nil
	}

	// Build a single SQL query that matches any pattern via LIKE 'pattern'.
	likeClause := make([]string, len(patterns))
	for i, p := range patterns {
		likeClause[i] = fmt.Sprintf("table_name LIKE '%s'", strings.ReplaceAll(p, "'", "''"))
	}
	query := fmt.Sprintf(
		"SELECT DISTINCT table_name FROM information_schema.tables WHERE table_schema='%s' AND (%s) ORDER BY table_name;",
		c.sourceDB, strings.Join(likeClause, " OR "),
	)

	// Use plesk db for the query (uses root MySQL access).
	out, err := run("/usr/sbin/plesk", "db", query)
	if err != nil {
		return nil, fmt.Errorf("querying schema-only tables: %v\n%s", err, out)
	}

	var tables []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// Skip MySQL ASCII table borders / header
		if line == "" || strings.HasPrefix(line, "+") || line == "table_name" {
			continue
		}
		// Strip the surrounding | characters from the plesk db ASCII table
		line = strings.Trim(line, "| ")
		line = strings.TrimSpace(line)
		if line != "" {
			tables = append(tables, line)
		}
	}
	return tables, nil
}

// mysqlExec executes a SQL statement against the target database using the
// target DB user credentials.
func mysqlExec(c *config, sql string) error {
	if c.dryRun {
		preview := sql
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		infof("  [dry-run] mysql: %s", preview)
		return nil
	}
	args := []string{
		"--user=" + c.targetDBUser,
		"--password=" + c.targetDBPass,
		"-h", c.sourceDBHost,
		c.targetDB,
		"-e", sql,
	}
	out, err := run("mysql", args...)
	if err != nil {
		return fmt.Errorf("mysql exec failed: %v\n%s", err, out)
	}
	return nil
}

// mysqldumpPipe performs a streaming dump from source DB to target DB.
//
// Two-pass strategy:
//   1. Dump full schema (CREATE TABLEs, triggers, routines) for ALL tables.
//   2. Dump data only (INSERTs) for tables NOT in the schema-only list.
//
// The two streams are concatenated and piped directly into the target mysql
// client, avoiding any temporary file on disk.
//
// Tables in the schema-only list will have their structure created but no
// data inserted (they will be empty).
func mysqldumpPipe(c *config, schemaOnlyTables []string) error {
	// Build ignore-table args for the data pass.
	var ignoreArgs []string
	for _, t := range schemaOnlyTables {
		ignoreArgs = append(ignoreArgs, "--ignore-table="+c.sourceDB+"."+t)
	}

	// Source credentials come from env.php (we already loaded them).
	srcUser := c.sourceDBUser
	srcPass := c.sourceDBPass
	srcHost := c.sourceDBHost

	// Pass 1: schema for ALL tables (with triggers + routines).
	schemaArgs := []string{
		"--single-transaction",
		"--quick",
		"--no-data",
		"--routines",
		"--triggers",
		"--no-tablespaces",
		"--skip-add-locks",
		"--skip-disable-keys",
		"--user=" + srcUser,
		"--password=" + srcPass,
		"-h", srcHost,
		c.sourceDB,
	}

	// Pass 2: data only for non-excluded tables.
	dataArgs := []string{
		"--single-transaction",
		"--quick",
		"--no-create-info",
		"--skip-triggers",
		"--no-tablespaces",
		"--skip-add-locks",
		"--skip-disable-keys",
		"--user=" + srcUser,
		"--password=" + srcPass,
		"-h", srcHost,
		c.sourceDB,
	}
	dataArgs = append(dataArgs, ignoreArgs...)

	// Target mysql client args.
	targetArgs := []string{
		"--user=" + c.targetDBUser,
		"--password=" + c.targetDBPass,
		"-h", c.sourceDBHost,
		c.targetDB,
	}

	c.verbosef("mysqldump pass 1 (schema for all tables)")
	totalTables := countDataTables(c, schemaOnlyTables)
	if totalTables >= 0 {
		c.verbosef("mysqldump pass 2 (data for %d tables, schema-only for %d tables)",
			totalTables-len(schemaOnlyTables), len(schemaOnlyTables))
	} else {
		c.verbosef("mysqldump pass 2 (data for non-excluded tables, schema-only for %d tables)",
			len(schemaOnlyTables))
	}

	if c.dryRun {
		infof("  [dry-run] would pipe mysqldump (2 passes, %d schema-only tables) into mysql %s",
			len(schemaOnlyTables), c.targetDB)
		return nil
	}

	// We run two mysqldump commands in sequence and pipe both outputs into
	// a single mysql command. The simplest portable way: spawn three
	// processes connected with pipes:
	//
	//   mysqldump(schema) ─┐
	//                      ├─→ cat ──→ mysql (target)
	//   mysqldump(data)  ─┘
	//
	// We do this by:
	//   1. Run mysqldump pass 1, capture stdout.
	//   2. Run mysqldump pass 2, capture stdout.
	//   3. Pipe both (concatenated) into mysql.
	//
	// For large DBs this uses memory = dump size. To avoid that, we use
	// bash with process substitution: { cmd1; cmd2; } | cmd3.

	// Build the bash pipeline. We use bash -c to allow command grouping.
	pass1 := "mysqldump " + shellQuoteMany(schemaArgs)
	pass2 := "mysqldump " + shellQuoteMany(dataArgs)
	mysqlCmd := "mysql " + shellQuoteMany(targetArgs)
	bashCmd := "{ " + pass1 + " ; " + pass2 + " ; } | " + mysqlCmd

	infof("  streaming schema + data (this may take a few minutes)...")
	out, err := run("/bin/bash", "-c", bashCmd)
	if err != nil {
		return fmt.Errorf("mysqldump pipe failed: %v\n%s", err, out)
	}
	return nil
}

// countDataTables returns the total number of tables in the source database.
// Used for verbose logging (caller subtracts schema-only count).
func countDataTables(c *config, schemaOnlyTables []string) int {
	out, err := run("/usr/sbin/plesk", "db",
		fmt.Sprintf("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='%s';", c.sourceDB))
	if err != nil {
		return -1
	}
	// Parse the ASCII table output - the number is on the last data row.
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "COUNT") {
			continue
		}
		line = strings.Trim(line, "| ")
		var n int
		fmt.Sscanf(line, "%d", &n)
		return n
	}
	return -1
}

// writeSQLFile writes SQL to a temp file and returns the path. Useful for
// debugging.
func writeSQLFile(c *config, name, sql string) (string, error) {
	path := "/tmp/magento-staging-" + name + ".sql"
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.WriteString(f, sql); err != nil {
		return "", err
	}
	c.verbosef("wrote SQL to %s", path)
	return path, nil
}

// applyStagingConfigSQL runs the core_config_data UPDATEs needed to make
// the staging site behave as a staging site (URLs, SMTP, SEO, ES prefix,
// cron disable, payments off, analytics off).
func applyStagingConfigSQL(c *config) error {
	stagingURL := c.stagingURL()
	sqls := []string{}

	// URLs
	sqls = append(sqls, fmt.Sprintf(
		"UPDATE core_config_data SET value='%s' WHERE path IN ('web/unsecure/base_url','web/secure/base_url');",
		stagingURL))
	sqls = append(sqls,
		"UPDATE core_config_data SET value=NULL WHERE path IN ('web/unsecure/base_link_url','web/secure/base_link_url','web/unsecure/base_media_url','web/secure/base_media_url','web/unsecure/base_static_url','web/secure/base_static_url');")

	// SMTP disable
	if !c.skipEmailDisable {
		sqls = append(sqls,
			"INSERT INTO core_config_data (scope,scope_id,path,value) VALUES ('default',0,'system/smtp/disable',1) ON DUPLICATE KEY UPDATE value=1;")
	}

	// SEO NOINDEX
	if !c.skipSEODisable {
		sqls = append(sqls,
			"UPDATE core_config_data SET value='NOINDEX,NOFOLLOW' WHERE path='design/search_engine_robots/default_robots';")
	}

	// Elasticsearch prefixes (only if suffix is non-empty)
	if c.elasticSuffix != "" {
		sqls = append(sqls, fmt.Sprintf(
			"UPDATE core_config_data SET value=CONCAT(value,'%s') WHERE path='catalog/search/elasticsearch6_index_prefix' AND value IS NOT NULL AND value NOT LIKE '%%%s';",
			c.elasticSuffix, c.elasticSuffix))
		sqls = append(sqls, fmt.Sprintf(
			"UPDATE core_config_data SET value=CONCAT(value,'%s') WHERE path='catalog/search/elasticsearch7_index_prefix' AND value IS NOT NULL AND value NOT LIKE '%%%s';",
			c.elasticSuffix, c.elasticSuffix))
		sqls = append(sqls, fmt.Sprintf(
			"UPDATE core_config_data SET value=CONCAT(value,'%s') WHERE path='amasty_elastic/connection/index_prefix' AND value IS NOT NULL AND value NOT LIKE '%%%s';",
			c.elasticSuffix, c.elasticSuffix))
	}

	// Cron disable
	if !c.skipCronDisable {
		sqls = append(sqls,
			"INSERT INTO core_config_data (scope,scope_id,path,value) VALUES ('default',0,'dev/cron/disable',1) ON DUPLICATE KEY UPDATE value=1;")
	}

	// Payment methods disable
	if !c.skipPaymentDisable {
		sqls = append(sqls,
			"UPDATE core_config_data SET value=0 WHERE path LIKE 'payment/%/active' AND value=1;")
	}

	// Analytics disable
	if !c.skipAnalyticsDisable {
		sqls = append(sqls,
			"UPDATE core_config_data SET value=0 WHERE path LIKE '%google/analytics/active' OR path LIKE '%facebook_pixel/%/active' OR path LIKE '%gtm/%/active';")
	}

	for _, sql := range sqls {
		if err := mysqlExec(c, sql); err != nil {
			return err
		}
	}
	return nil
}

// fetchOriginalESPrefixes reads the current ES prefixes from the source DB
// so we can report them and confirm they will change.
func fetchOriginalESPrefixes(c *config) {
	q := fmt.Sprintf("SELECT path, value FROM %s.core_config_data WHERE path IN ('catalog/search/elasticsearch6_index_prefix','catalog/search/elasticsearch7_index_prefix','amasty_elastic/connection/index_prefix');", c.sourceDB)
	out, err := run("/usr/sbin/plesk", "db", q)
	if err != nil {
		c.verbosef("fetchOriginalESPrefixes query failed: %v", err)
		return
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "path") {
			continue
		}
		line = strings.Trim(line, "| ")
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		path := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch path {
		case "catalog/search/elasticsearch6_index_prefix":
			c.originalES6Prefix = val
		case "catalog/search/elasticsearch7_index_prefix":
			c.originalES7Prefix = val
		case "amasty_elastic/connection/index_prefix":
			c.originalAmastyPrefix = val
		}
	}
	c.verbosef("ES prefixes: ES6=%s ES7=%s Amasty=%s",
		c.originalES6Prefix, c.originalES7Prefix, c.originalAmastyPrefix)
}

// verifyStagingDB writes a single SQL check to verify the staging URL was set.
func verifyStagingDB(c *config) error {
	if c.dryRun {
		infof("  [dry-run] verify staging URL in core_config_data")
		return nil
	}
	q := "SELECT value FROM core_config_data WHERE path='web/secure/base_url';"
	out, err := run("mysql",
		"--user="+c.targetDBUser, "--password="+c.targetDBPass,
		"-h", c.sourceDBHost, c.targetDB, "-e", q)
	if err != nil {
		return fmt.Errorf("verify query failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, c.stagingURL()) {
		return fmt.Errorf("staging URL not set correctly; got: %s", out)
	}
	return nil
}
