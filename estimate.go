package main

import (
	"fmt"
	"strconv"
	"strings"
)

// sizeEstimate holds the disk-space estimates for staging creation.
type sizeEstimate struct {
	// Files (httpdocs)
	SourceFilesBytes int64
	StagingFilesBytes int64
	FilesExcludedBytes int64

	// Database
	SourceDBBytes int64
	StagingDBBytes int64
	DBSchemaOnlyBytes int64

	// Schema-only table list (resolved)
	SchemaOnlyTableCount int
}

// humanSize converts a byte count to a human-readable string.
func humanSize(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case b >= TB:
		return fmt.Sprintf("%.2f TB", float64(b)/float64(TB))
	case b >= GB:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// parseDuOutput extracts the byte count from the first field of `du -b` output.
// `du -b` (apparent size) prints "<bytes>\t<path>".
func parseDuOutput(out string) int64 {
	out = strings.TrimSpace(out)
	if out == "" {
		return 0
	}
	// Take everything before the first tab
	idx := strings.IndexByte(out, '\t')
	if idx > 0 {
		out = out[:idx]
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	return n
}

// estimateFilesSize runs `du` on the source path with the rsync exclude
// patterns to compute:
//   - Total source httpdocs size (no excludes)
//   - Staging httpdocs size (with excludes applied)
//   - Bytes excluded (difference)
func estimateFilesSize(c *config) (sourceTotal, stagingTotal int64, err error) {
	// Total source size (apparent bytes for accuracy)
	out, runErr := run("du", "-sb", c.sourcePath)
	if runErr != nil {
		return 0, 0, fmt.Errorf("du source failed: %v\n%s", runErr, out)
	}
	sourceTotal = parseDuOutput(out)

	// Staging size = source size with excludes applied
	// We use du --exclude=... (same patterns as rsync).
	duArgs := []string{"-sb"}
	for _, ex := range rsyncExcludes {
		if ex == ".git/" && c.includeGit {
			continue
		}
		// du --exclude expects a pattern; trailing /* is fine (du matches
		// directory entries).
		duArgs = append(duArgs, "--exclude="+ex)
	}
	duArgs = append(duArgs, c.sourcePath)

	out, runErr = run("du", duArgs...)
	if runErr != nil {
		// du may fail on permission errors; fall back to estimate
		c.verbosef("du with excludes failed: %v\n%s", runErr, out)
		return sourceTotal, sourceTotal, nil
	}
	stagingTotal = parseDuOutput(out)

	return sourceTotal, stagingTotal, nil
}

// estimateDBSize queries information_schema for:
//   - Total source DB size (data + index for all tables)
//   - Staging DB size (total minus data_length of schema-only tables;
//     schema-only tables still occupy a small amount of space for the empty
//     structure, but their data is skipped).
func estimateDBSize(c *config, schemaOnlyTables []string) (sourceTotal, stagingTotal int64, err error) {
	// Total source DB size
	q := fmt.Sprintf(
		"SELECT IFNULL(SUM(data_length+index_length),0) FROM information_schema.tables WHERE table_schema='%s';",
		c.sourceDB,
	)
	out, runErr := run("/usr/sbin/plesk", "db", q)
	if runErr != nil {
		return 0, 0, fmt.Errorf("source DB size query failed: %v\n%s", runErr, out)
	}
	sourceTotal = parsePLESKDBNumber(out)

	if len(schemaOnlyTables) == 0 {
		return sourceTotal, sourceTotal, nil
	}

	// Sum of data_length+index_length for schema-only tables (this is what
	// we skip).
	// Build the IN (...) clause. MySQL has a limit of 65535 placeholders but
	// we will not hit it; fall back to chunking if ever needed.
	var quoted []string
	for _, t := range schemaOnlyTables {
		quoted = append(quoted, "'"+strings.ReplaceAll(t, "'", "''")+"'")
	}
	// Chunk to avoid query length limits (1000 tables per chunk).
	chunkSize := 1000
	var schemaOnlyBytes int64
	for i := 0; i < len(quoted); i += chunkSize {
		end := i + chunkSize
		if end > len(quoted) {
			end = len(quoted)
		}
		q = fmt.Sprintf(
			"SELECT IFNULL(SUM(data_length+index_length),0) FROM information_schema.tables WHERE table_schema='%s' AND table_name IN (%s);",
			c.sourceDB, strings.Join(quoted[i:end], ","),
		)
		out, runErr = run("/usr/sbin/plesk", "db", q)
		if runErr != nil {
			c.verbosef("schema-only size query failed: %v\n%s", runErr, out)
			continue
		}
		schemaOnlyBytes += parsePLESKDBNumber(out)
	}

	// Staging DB = source - data skipped + small overhead for empty tables
	// (we still create the structure, but it's negligible).
	stagingTotal = sourceTotal - schemaOnlyBytes
	if stagingTotal < 0 {
		stagingTotal = 0
	}
	return sourceTotal, stagingTotal, nil
}

// parsePLESKDBNumber extracts the numeric value from `plesk db` ASCII table
// output (handles the +----+ borders and column headers).
//
// `plesk db` returns output like:
//   +-----------------------------------------+
//   | IFNULL(SUM(data_length+index_length),0) |
//   +-----------------------------------------+
//   |                              3142369280 |
//   +-----------------------------------------+
//
// We want the last numeric row (skip borders and the header row).
func parsePLESKDBNumber(out string) int64 {
	var lastNumber int64
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "+") {
			continue
		}
		// Strip surrounding | characters
		line = strings.Trim(line, "| ")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Try to parse as number; if it parses, it's the data row.
		// MySQL returns integers, possibly with .00 decimal for SUM.
		f, err := strconv.ParseFloat(line, 64)
		if err != nil {
			// Not a number - this is the column header row; skip.
			continue
		}
		lastNumber = int64(f)
	}
	return lastNumber
}

// estimateStagingSize computes the full size estimate (files + DB).
// This is read-only (no changes to the system).
func estimateStagingSize(c *config) (*sizeEstimate, error) {
	est := &sizeEstimate{}

	// Files
	srcFiles, stgFiles, err := estimateFilesSize(c)
	if err != nil {
		return nil, fmt.Errorf("files estimate: %v", err)
	}
	est.SourceFilesBytes = srcFiles
	est.StagingFilesBytes = stgFiles
	est.FilesExcludedBytes = srcFiles - stgFiles

	// Schema-only tables (need them for DB estimate)
	schemaOnlyTables, err := getSchemaOnlyTables(c)
	if err != nil {
		warnf("could not determine schema-only tables: %v (continuing with full data)", err)
		schemaOnlyTables = nil
	}
	est.SchemaOnlyTableCount = len(schemaOnlyTables)

	// DB
	srcDB, stgDB, err := estimateDBSize(c, schemaOnlyTables)
	if err != nil {
		return nil, fmt.Errorf("DB estimate: %v", err)
	}
	est.SourceDBBytes = srcDB
	est.StagingDBBytes = stgDB
	est.DBSchemaOnlyBytes = srcDB - stgDB

	return est, nil
}

// printSizeEstimate prints the disk-space estimate comparison.
func printSizeEstimate(c *config, est *sizeEstimate) {
	infof("")
	infof("=== Disk space estimate ===")
	infof("")
	infof("  FILES (httpdocs):")
	infof("    Source (live):       %s", humanSize(est.SourceFilesBytes))
	infof("    Staging (with excludes): %s", humanSize(est.StagingFilesBytes))
	if est.FilesExcludedBytes > 0 {
		infof("    Excluded (caches/logs/media cache/.git): %s  (%.1f%% reduction)",
			humanSize(est.FilesExcludedBytes),
			100.0*float64(est.FilesExcludedBytes)/float64(est.SourceFilesBytes))
	}
	infof("")
	infof("  DATABASE:")
	infof("    Source (live):       %s", humanSize(est.SourceDBBytes))
	infof("    Staging (schema-only tables empty): %s", humanSize(est.StagingDBBytes))
	if est.DBSchemaOnlyBytes > 0 {
		infof("    Skipped data (schema-only %d tables): %s  (%.1f%% reduction)",
			est.SchemaOnlyTableCount,
			humanSize(est.DBSchemaOnlyBytes),
			100.0*float64(est.DBSchemaOnlyBytes)/float64(est.SourceDBBytes))
	}
	infof("")
	totalStaging := est.StagingFilesBytes + est.StagingDBBytes
	totalSource := est.SourceFilesBytes + est.SourceDBBytes
	infof("  TOTAL STAGING footprint: %s  (vs source %s — %.1f%% of live)",
		humanSize(totalStaging),
		humanSize(totalSource),
		100.0*float64(totalStaging)/float64(totalSource))
	infof("")
}
