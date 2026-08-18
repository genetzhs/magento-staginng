package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// envPHP is a type alias for a generic nested map (parsed env.php).
// Using a type ALIAS (not a type definition) so that type assertions with
// map[string]interface{} work correctly (Go distinguishes named types from
// unnamed types in type assertions).
type envPHP = map[string]interface{}

// loadEnvPHP reads env.php via the PHP CLI and returns the parsed structure.
// We use `php -r 'echo json_encode(require $file);'` for a round-trip-safe
// parse, then re-encode to PHP via var_export in writeEnvPHP.
func loadEnvPHP(phpBin, envPath string) (envPHP, error) {
	if !pathExists(envPath) {
		return nil, fmt.Errorf("env.php not found: %s", envPath)
	}
	script := fmt.Sprintf(`echo json_encode(require %s);`, shellQuote(envPath))
	out, err := run(phpBin, "-r", script)
	if err != nil {
		return nil, fmt.Errorf("php failed to read env.php: %v\n%s", err, out)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, fmt.Errorf("empty output from php for env.php")
	}
	var env envPHP
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		return nil, fmt.Errorf("failed to parse env.php JSON: %v", err)
	}
	return env, nil
}

// writeEnvPHP writes the env map back as a PHP array file. We use a small
// PHP script that takes JSON on stdin and writes a var_export() representation.
// This is round-trip safe (var_export produces valid PHP for nested arrays).
func writeEnvPHP(phpBin, envPath string, env envPHP) error {
	jsonBytes, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("failed to marshal env to JSON: %v", err)
	}
	// PHP script reads JSON from stdin, decodes, writes var_export to envPath.
	phpScript := `
$json = stream_get_contents(STDIN);
$data = json_decode($json, true);
if ($data === null && json_last_error() !== JSON_ERROR_NONE) { fwrite(STDERR, "json decode error: ".json_last_error_msg()."\n"); exit(1); }
$out = "<?php\nreturn " . var_export($data, true) . ";\n";
$rc = file_put_contents($argv[1], $out);
if ($rc === false) { fwrite(STDERR, "write failed\n"); exit(2); }
chmod($argv[1], 0640);
`
	out, err := runStdin(string(jsonBytes), phpBin, "-r", phpScript, envPath)
	if err != nil {
		return fmt.Errorf("php failed to write env.php: %v\n%s", err, out)
	}
	return nil
}

// getString navigates a nested map[string]interface{} and returns a string.
func getString(env envPHP, keys ...string) string {
	var cur interface{} = env
	for _, k := range keys {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return ""
		}
		cur, ok = m[k]
		if !ok {
			return ""
		}
	}
	switch v := cur.(type) {
	case string:
		return v
	case float64:
		// JSON numbers come as float64
		return fmt.Sprintf("%v", v)
	case bool:
		if v {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// setString navigates a nested map and sets a string value, creating
// intermediate maps as needed.
func setString(env envPHP, value string, keys ...string) {
	var cur interface{} = env
	for i, k := range keys {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return
		}
		if i == len(keys)-1 {
			m[k] = value
			return
		}
		next, exists := m[k]
		if !exists {
			next = map[string]interface{}{}
			m[k] = next
		}
		if _, ok := next.(map[string]interface{}); !ok {
			// Replace non-map with a map
			next = map[string]interface{}{}
			m[k] = next
		}
		cur = next
	}
}

// patchEnv applies the staging-specific changes to the env.php structure.
// Returns the new envPHP and the original values that were changed (for
// reporting).
func patchEnv(env envPHP, c *config) envPHP {
	// DB connection
	setString(env, c.targetDB, "db", "connection", "default", "dbname")
	setString(env, c.targetDBUser, "db", "connection", "default", "username")
	setString(env, c.targetDBPass, "db", "connection", "default", "password")

	// Redis id_prefix for default cache
	c.originalRedisPrefix = getString(env, "cache", "frontend", "default", "id_prefix")
	setString(env, c.redisIDPrefix, "cache", "frontend", "default", "id_prefix")

	// Redis id_prefix for page_cache (if exists)
	pageCachePrefix := getString(env, "cache", "frontend", "page_cache", "id_prefix")
	if pageCachePrefix != "" {
		setString(env, c.redisIDPrefix, "cache", "frontend", "page_cache", "id_prefix")
	}

	// Optionally set MAGE_MODE
	if c.magentoMode != "" {
		setString(env, c.magentoMode, "MAGE_MODE")
	}

	return env
}

// readSourceEnvAndDerive loads env.php from the source installation and
// populates the config struct with derived values (DB name, user, pass,
// php bin, MAGE_MODE, redis prefix, etc).
func readSourceEnvAndDerive(c *config) error {
	envPath := c.sourcePath + "/app/etc/env.php"

	// First, detect the PHP binary to use for parsing env.php.
	// We can't read env.php via PHP yet (chicken-and-egg), so:
	//   1. If --php-bin was given, use it.
	//   2. Look for 'php_executable_path' in env.php via grep (single line).
	//   3. Otherwise probe /opt/plesk/php/*/bin/php in version-desc order.
	//   4. Fall back to /usr/bin/php.
	if c.phpBin == "" || c.phpBin == "/usr/bin/php" {
		if detected := detectPHPBin(c, envPath); detected != "" {
			c.phpBin = detected
		}
	}

	env, err := loadEnvPHP(c.phpBin, envPath)
	if err != nil {
		return err
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

	// php_executable_path from env.php (now that we parsed it)
	phpExec := getString(env, "php_executable_path")
	if phpExec != "" {
		c.phpBin = phpExec
	}

	// Derive target DB name if not provided
	if c.targetDB == "" {
		// Plesk: DB names must be unique per server. We use suffix 'stg'
		// (not underscore which Plesk disallows in some contexts).
		c.targetDB = c.sourceDB + "stg"
	}
	if c.targetDBUser == "" {
		// Plesk DB user naming follows: <db>_user convention or similar.
		// We mirror source user naming with stg suffix.
		c.targetDBUser = c.sourceDBUser + "stg"
	}
	if c.redisIDPrefix == "" {
		// Default: original prefix + "stg_"
		orig := c.originalRedisPrefix
		if orig == "" {
			orig = "0_"
		}
		c.redisIDPrefix = orig + "stg_"
	}

	return nil
}

// detectPHPBin tries to find a PHP binary with the json extension enabled.
// Order of preference:
//   1. php_executable_path from env.php (via grep, since we can't parse yet)
//   2. /opt/plesk/php/<version>/bin/php (highest version first)
//   3. /usr/bin/php (last resort, may not have json extension)
func detectPHPBin(c *config, envPath string) string {
	// 1. Try to extract php_executable_path from env.php via grep/sed
	if out, err := run("grep", "-E", "'php_executable_path'[[:space:]]*=>", envPath); err == nil && out != "" {
		// Use sed to extract the path value from the line
		if extracted, err := run("sed", "-n", "s/.*'php_executable_path'[[:space:]]*=>[[:space:]]*\\([^'\"[:space:]]*\\).*/\\1/p", envPath); err == nil {
			path := strings.TrimSpace(extracted)
			if path != "" && pathExists(path) {
				c.verbosef("detected php_executable_path from env.php: %s", path)
				return path
			}
		}
	}

	// 2. Probe /opt/plesk/php/<ver>/bin/php in version-desc order
	if entries, err := os.ReadDir("/opt/plesk/php"); err == nil {
		var versions []string
		for _, e := range entries {
			if e.IsDir() {
				versions = append(versions, e.Name())
			}
		}
		// Sort descending
		for i := 0; i < len(versions); i++ {
			for j := i + 1; j < len(versions); j++ {
				if versions[j] > versions[i] {
					versions[i], versions[j] = versions[j], versions[i]
				}
			}
		}
		for _, v := range versions {
			path := "/opt/plesk/php/" + v + "/bin/php"
			if !pathExists(path) {
				continue
			}
			// Verify json extension is loaded
			if out, err := run(path, "-m"); err == nil && strings.Contains(out, "json") {
				c.verbosef("detected Plesk PHP binary: %s", path)
				return path
			}
		}
	}

	// 3. Last resort: /usr/bin/php (may not have json, but try)
	c.verbosef("falling back to /usr/bin/php")
	return "/usr/bin/php"
}

// ensureCredsDir creates the credentials directory with secure perms.
func ensureCredsDir(c *config) error {
	dir := "/var/www/vhosts/" + c.domain + "/.credentials"
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.Chmod(dir, 0700)
}
