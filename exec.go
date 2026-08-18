package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// run executes a command and returns combined output. If the command fails
// the caller decides whether to bail out.
func run(name string, args ...string) (string, error) {
	return runStdin("", name, args...)
}

// runStdin runs a command with optional stdin input.
func runStdin(stdin, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// mustRun runs a command and exits on failure.
func mustRun(c *config, name string, args ...string) string {
	out, err := run(name, args...)
	if err != nil {
		c.verbosef("command failed: %s %s", name, strings.Join(args, " "))
		c.verbosef("output: %s", out)
		failf("%s failed: %v\n%s", name, err, out)
	}
	c.verbosef("$ %s %s", name, strings.Join(args, " "))
	return out
}

// runQuiet runs a command, returning output and error (caller handles).
func runQuiet(name string, args ...string) (string, error) {
	out, err := run(name, args...)
	if err != nil {
		return out, err
	}
	return strings.TrimSpace(out), nil
}

// runAsUser runs a command as a specific system user via runuser.
func runAsUser(c *config, user, dir, name string, args ...string) (string, error) {
	runuserArgs := []string{"-u", user, "--"}
	if dir != "" {
		// Use bash -c so we can cd
		cmd := fmt.Sprintf("cd %s && exec %s %s", shellQuote(dir), name, shellQuoteMany(args))
		runuserArgs = append(runuserArgs, "/bin/bash", "-c", cmd)
	} else {
		runuserArgs = append(runuserArgs, name)
		runuserArgs = append(runuserArgs, args...)
	}
	return run("runuser", runuserArgs...)
}

// shellQuote returns s wrapped in single quotes, escaping inner single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// shellQuoteMany quotes each arg and joins with spaces.
func shellQuoteMany(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}

// pathExists checks if a path exists.
func pathExists(p string) bool {
	_, err := runQuiet("test", "-e", p)
	return err == nil
}

// pathIsEmpty checks if a directory is empty or non-existent.
func pathIsEmpty(p string) bool {
	out, err := run("ls", "-A", p)
	if err != nil {
		return true // non-existent counts as empty
	}
	return strings.TrimSpace(out) == ""
}
