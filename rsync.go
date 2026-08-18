package main

import (
	"fmt"
	"strings"
)

// rsyncCopy copies the source httpdocs to the target staging path using rsync
// with the canonical exclude list. The target directory is the staging root
// directly (e.g. /var/www/vhosts/<domain>/staging/) — NOT staging/httpdocs/,
// because we want the staging path itself to be the document root.
func rsyncCopy(c *config) error {
	// Ensure target path exists (Plesk subdomain --create should have
	// created it but we make sure). Skip in dry-run mode.
	if !c.dryRun && !pathExists(c.targetPath) {
		mustRun(c, "mkdir", "-p", c.targetPath)
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
	// rsync source/httpdocs/ -> target/  (contents of httpdocs go directly
	// into the staging root, which is the document root).
	args = append(args, c.sourcePath+"/", c.targetPath+"/")

	if c.dryRun {
		printInfo("[dry-run] rsync %d excludes -> %s", len(rsyncExcludes), c.targetPath)
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
		humanSize(dirSize(c.targetPath)), c.targetPath))
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
