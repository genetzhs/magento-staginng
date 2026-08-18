package main

import (
	"fmt"
	"strings"
)

// rsyncCopy copies the source httpdocs to the target httpdocs using rsync
// with the canonical exclude list. The target directory must exist.
func rsyncCopy(c *config) error {
	// Ensure target httpdocs exists (Plesk subdomain --create should have
	// created it but we make sure). Skip in dry-run mode.
	targetHttpdocs := c.targetPath + "/httpdocs"
	if !c.dryRun && !pathExists(targetHttpdocs) {
		mustRun(c, "mkdir", "-p", targetHttpdocs)
	}

	args := []string{
		"-a",
		"--info=progress2",
	}
	for _, ex := range rsyncExcludes {
		// Allow --include-git override
		if ex == ".git/" && c.includeGit {
			continue
		}
		args = append(args, "--exclude="+ex)
	}
	args = append(args, c.sourcePath+"/", targetHttpdocs+"/")

	if c.dryRun {
		infof("  [dry-run] rsync %d excludes -> %s", len(rsyncExcludes), targetHttpdocs)
		return nil
	}

	c.verbosef("$ rsync %s", strings.Join(args, " "))
	out, err := run("rsync", args...)
	if err != nil {
		// rsync prints progress to stdout/stderr - keep last bit
		lines := strings.Split(out, "\n")
		tail := strings.Join(lines[max(0, len(lines)-5):], "\n")
		return fmt.Errorf("rsync failed: %v\n%s", err, tail)
	}
	// rsync progress goes to stderr (out has combined output)
	return nil
}

// max returns the larger of a or b.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
