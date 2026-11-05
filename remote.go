package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// defaultSSHKey is the private key used to reach source (live) servers
// in migrate mode unless --ssh-key is given.
const defaultSSHKey = "/root/.ssh/migration_rsa"

// sshOptions returns the non-interactive ssh options shared by plain ssh
// calls and rsync's remote shell. BatchMode makes a missing/rejected key
// fail fast instead of prompting for a password.
func sshOptions(c *config) []string {
	return []string{
		"-i", c.sshKey,
		"-p", c.remotePort,
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=30",
	}
}

// remoteSpec returns user@host for the source server.
func remoteSpec(c *config) string {
	return c.remoteUser + "@" + c.remoteHost
}

// sshCommandArgs builds the ssh argv that runs the given command on the
// source server. The remote side parses the command with a shell, so every
// argument is single-quoted.
func sshCommandArgs(c *config, extraOpts []string, remoteCmd string) []string {
	args := append([]string{}, sshOptions(c)...)
	args = append(args, extraOpts...)
	args = append(args, remoteSpec(c), "--", remoteCmd)
	return args
}

// srcRun runs a command on the SOURCE side: locally for `create`, over
// SSH on the live server for `migrate`. Only read-only commands are run
// on the source (plesk db SELECTs, du, rsync -n, php -r reading env.php,
// mysqldump).
func srcRun(c *config, name string, args ...string) (string, error) {
	if !c.isRemote() {
		return run(name, args...)
	}
	remoteCmd := shellQuote(name)
	if len(args) > 0 {
		remoteCmd += " " + shellQuoteMany(args)
	}
	c.verbosef("[%s] $ %s %s", c.remoteHost, name, redactArgs(args))
	return run("ssh", sshCommandArgs(c, nil, remoteCmd)...)
}

// redactArgs joins args for logging, masking --password=... values.
func redactArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "--password=") {
			a = "--password=***"
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

// srcPleskQuery runs a read-only query against the source server's psa
// database (via `plesk db`) and returns the first field of the last row.
func srcPleskQuery(c *config) pleskQueryFn {
	return func(query string) (string, error) {
		out, err := srcRun(c, "/usr/sbin/plesk", "db", query)
		if err != nil {
			return "", fmt.Errorf("plesk db on %s failed: %v\n%s", c.remoteHost, err, out)
		}
		return dbFirstField(out), nil
	}
}

// srcPathExists checks a path on the source side.
func srcPathExists(c *config, p string) bool {
	_, err := srcRun(c, "test", "-e", p)
	return err == nil
}

// checkSSH verifies key-based SSH access to the source and that the remote
// user can query Plesk (root is required for `plesk db`).
func checkSSH(c *config) error {
	if !pathExists(c.sshKey) {
		return fmt.Errorf("ssh key not found: %s (use --ssh-key)", c.sshKey)
	}
	out, err := srcRun(c, "/usr/sbin/plesk", "version")
	if err != nil {
		return fmt.Errorf("cannot run 'plesk version' on %s as %s: %v\n%s",
			remoteSpec(c), c.remoteUser, err, tail(out, 5))
	}
	return nil
}

// resolveRemoteSource locates the live Magento installation on the source
// server through its Plesk database (document root + PHP handler), the
// same way resolvePaths does locally.
func resolveRemoteSource(c *config) error {
	q := srcPleskQuery(c)
	c.phpHandlerID = pleskPHPHandlerIDQ(q, c.domain)

	if c.sourcePath == "" {
		docroot, err := pleskDocumentRootQ(q, c.domain)
		if err != nil {
			return fmt.Errorf("could not resolve the document root of %s on %s: %v (pass --source-path)",
				c.domain, c.remoteHost, err)
		}
		c.remoteDocRoot = docroot
		docroot = strings.TrimRight(docroot, "/")
		switch {
		case srcPathExists(c, docroot+"/app/etc/env.php"):
			c.sourcePath = docroot
		case srcPathExists(c, parentDir(docroot)+"/app/etc/env.php"):
			c.sourcePath = parentDir(docroot)
		default:
			return fmt.Errorf("no Magento installation (app/etc/env.php) at or above %s on %s (pass --source-path)",
				docroot, c.remoteHost)
		}
	}
	c.sourcePath = strings.TrimRight(c.sourcePath, "/")
	if !srcPathExists(c, c.sourcePath+"/app/etc/env.php") {
		return fmt.Errorf("source is not a Magento installation (no app/etc/env.php in %s on %s)",
			c.sourcePath, c.remoteHost)
	}

	// PHP CLI on the source, only used to parse env.php.
	c.remotePHPBin = "php"
	if bin := phpBinFromHandlerID(c.phpHandlerID); bin != "" && srcPathExists(c, bin) {
		c.remotePHPBin = bin
	}
	return nil
}

// parentDir returns the parent directory of an absolute path.
func parentDir(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "/"
}

// readRemoteEnvAndDerive parses the source env.php over SSH and fills the
// source DB credentials and derived staging defaults.
func readRemoteEnvAndDerive(c *config) error {
	envPath := c.sourcePath + "/app/etc/env.php"
	script := fmt.Sprintf(`echo json_encode(require %s);`, shellQuote(envPath))
	out, err := srcRun(c, c.remotePHPBin, "-r", script)
	if err != nil {
		return fmt.Errorf("reading %s on %s failed: %v\n%s", envPath, c.remoteHost, err, tail(out, 5))
	}
	var env envPHP
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		return fmt.Errorf("failed to parse remote env.php JSON: %v", err)
	}

	c.sourceDB = getString(env, "db", "connection", "default", "dbname")
	c.sourceDBUser = getString(env, "db", "connection", "default", "username")
	c.sourceDBPass = getString(env, "db", "connection", "default", "password")
	c.sourceDBHost = getString(env, "db", "connection", "default", "host")
	if c.sourceDBHost == "" {
		c.sourceDBHost = "localhost"
	}
	c.sourceMageMode = getString(env, "MAGE_MODE")
	c.originalRedisPrefix = getString(env, "cache", "frontend", "default", "id_prefix")
	c.sourceAdminFrontName = getString(env, "backend", "frontName")

	if c.sourceDB == "" {
		return fmt.Errorf("no db.connection.default.dbname in remote env.php")
	}
	if c.targetDB == "" {
		c.targetDB = c.sourceDB + "stg"
	}
	if c.targetDBUser == "" {
		c.targetDBUser = c.sourceDBUser + "stg"
	}
	if c.redisIDPrefix == "" {
		orig := c.originalRedisPrefix
		if orig == "" {
			orig = "0_"
		}
		c.redisIDPrefix = orig + "stg_"
	}

	// Services referenced by env.php must also exist on THIS server.
	for _, h := range []struct{ label, val string }{
		{"Redis cache", getString(env, "cache", "frontend", "default", "backend_options", "server")},
		{"Redis page cache", getString(env, "cache", "frontend", "page_cache", "backend_options", "server")},
		{"Redis sessions", getString(env, "session", "redis", "host")},
	} {
		if h.val != "" {
			printInfo("%s host in env.php: %s (must be reachable from this server)", h.label, h.val)
		}
	}
	return nil
}

// mysqlClientOptionFile renders a [client] option file for the source DB.
// The DB host from env.php may be "host", "host:port" or a socket path.
func mysqlClientOptionFile(user, pass, host string) string {
	q := func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	var b strings.Builder
	b.WriteString("[client]\n")
	b.WriteString("user=" + q(user) + "\n")
	b.WriteString("password=" + q(pass) + "\n")
	switch {
	case strings.HasPrefix(host, "/"):
		b.WriteString("socket=" + q(host) + "\n")
	case strings.Count(host, ":") == 1:
		parts := strings.SplitN(host, ":", 2)
		if strings.HasPrefix(parts[1], "/") {
			b.WriteString("host=" + q(parts[0]) + "\n")
			b.WriteString("socket=" + q(parts[1]) + "\n")
		} else {
			b.WriteString("host=" + q(parts[0]) + "\n")
			b.WriteString("port=" + parts[1] + "\n")
		}
	default:
		b.WriteString("host=" + q(host) + "\n")
	}
	return b.String()
}

// remoteDumpScript returns the bash script executed on the source server
// (fed on ssh's stdin, so the DB password never appears in a process list
// on either side). It writes a private option file, runs both mysqldump
// passes to stdout and removes the option file on exit.
func remoteDumpScript(c *config, schemaOnlyTables []string) string {
	schemaArgs := []string{
		"--single-transaction", "--quick", "--no-data", "--routines", "--triggers",
		"--no-tablespaces", "--skip-add-locks", "--skip-disable-keys", c.sourceDB,
	}
	dataArgs := []string{
		"--single-transaction", "--quick", "--no-create-info", "--skip-triggers",
		"--no-tablespaces", "--skip-add-locks", "--skip-disable-keys", c.sourceDB,
	}
	for _, t := range schemaOnlyTables {
		dataArgs = append(dataArgs, "--ignore-table="+c.sourceDB+"."+t)
	}
	var b strings.Builder
	b.WriteString("set -e\numask 077\n")
	b.WriteString("cnf=$(mktemp)\ntrap 'rm -f \"$cnf\"' EXIT\n")
	b.WriteString("cat > \"$cnf\" <<'MAGENTO_STAGING_EOF'\n")
	b.WriteString(mysqlClientOptionFile(c.sourceDBUser, c.sourceDBPass, c.sourceDBHost))
	b.WriteString("MAGENTO_STAGING_EOF\n")
	b.WriteString("mysqldump --defaults-extra-file=\"$cnf\" " + shellQuoteMany(schemaArgs) + "\n")
	b.WriteString("mysqldump --defaults-extra-file=\"$cnf\" " + shellQuoteMany(dataArgs) + "\n")
	return b.String()
}

// mysqldumpPipeRemote streams the source database from the live server
// into the local staging database:
//
//	ssh source 'bash -s' (mysqldump x2) | sed (strip DEFINER) | mysql (local)
func mysqldumpPipeRemote(c *config, schemaOnlyTables []string) error {
	if c.dryRun {
		printInfo("[dry-run] would stream mysqldump from %s (%d schema-only tables) into mysql %s",
			c.remoteHost, len(schemaOnlyTables), c.targetDB)
		return nil
	}
	sshArgs := sshCommandArgs(c, []string{"-o", "Compression=yes"}, "bash -s")
	targetArgs := []string{
		"--user=" + c.targetDBUser,
		"--password=" + c.targetDBPass,
		"-h", c.targetDBHost,
		c.targetDB,
	}
	sedFilter := `sed -E 's@/\*![0-9]+ DEFINER=[^*]*\*/@@g'`
	bashCmd := "set -o pipefail; ssh " + shellQuoteMany(sshArgs) + " | " + sedFilter +
		" | mysql " + shellQuoteMany(targetArgs)

	sp := newSpinner(fmt.Sprintf("Streaming %s from %s -> mysql %s",
		c.sourceDB, c.remoteHost, c.targetDB))
	sp.Start()
	out, err := runStdin(remoteDumpScript(c, schemaOnlyTables), "/bin/bash", "-c", bashCmd)
	if err != nil {
		sp.Stop(false, fmt.Sprintf("remote mysqldump pipe failed: %v", err))
		return fmt.Errorf("remote mysqldump pipe failed: %v\n%s", err, tail(out, 20))
	}
	sp.Stop(true, fmt.Sprintf("database copied (%s schema-only tables)", boldInt(len(schemaOnlyTables))))
	return nil
}

// rsyncRemoteShell returns the -e value for rsync (ssh with our options).
func rsyncRemoteShell(c *config) string {
	return "ssh " + shellQuoteMany(sshOptions(c))
}
