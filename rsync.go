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
		printInfo("[dry-run] rsync %d excludes -> %s", len(rsyncExcludes), targetHttpdocs)
		return nil
	}

	sp := newSpinner(fmt.Sprintf("Copying files (rsync with %d excludes)", len(rsyncExcludes)))
	sp.Start()
	c.verbosef("$ rsync %s", strings.Join(args, " "))
	out, err := run("rsync", args...)
	if err != nil {
		// rsync prints progress to stdout/stderr - keep last bit
		lines := strings.Split(out, "\n")
		tail := strings.Join(lines[max(0, len(lines)-5):], "\n")
		sp.Stop(false, fmt.Sprintf("rsync failed: %v", err))
		return fmt.Errorf("rsync failed: %v\n%s", err, tail)
	}
	sp.Stop(true, fmt.Sprintf("files copied (%s -> %s)",
		humanSize(dirSize(targetHttpdocs)), targetHttpdocs))
	return nil
}

// dirSize returns the apparent size of a directory in bytes.
func dirSize(path string) int64 {
	out, err := run("du", "-sb", path)
	if err != nil {
		return 0
	}
	return parseDuOutput(out)
}

// max returns the larger of a or b.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
