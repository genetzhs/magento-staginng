package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// sizeEstimate holds the disk-space estimates for staging creation.
type sizeEstimate struct {
	// Files (httpdocs)
	SourceFilesBytes   int64
	StagingFilesBytes  int64
	FilesExcludedBytes int64

	// Database
	SourceDBBytes     int64
	StagingDBBytes    int64
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

// estimateFilesSize computes:
//   - Total source size (apparent bytes) via `du -sb`
//   - Staging size via `rsync -a -n --stats` with the SAME exclude
//     patterns as the real copy
//
// The staging size MUST come from rsync itself: the exclude patterns are
// anchored with a leading "/" (rsync transfer-root semantics, e.g.
// "/var/cache/*"), but `du --exclude` matches "/"-patterns against the
// entire absolute path — so du never matches them and would report
// ~zero savings. rsync --stats evaluates the patterns exactly like the
// real run and reports what would actually be transferred.
func estimateFilesSize(c *config) (sourceTotal, stagingTotal int64, err error) {
	// Total source size (apparent bytes for accuracy)
	out, runErr := run("du", "-sb", c.sourcePath)
	if runErr != nil {
		return 0, 0, fmt.Errorf("du source failed: %v\n%s", runErr, out)
	}
	sourceTotal = parseDuOutput(out)

	// Staging size = what rsync would copy with the excludes applied.
	args := []string{"-a", "-n", "--stats"}
	for _, ex := range rsyncExcludes {
		if ex == "/.git/" && c.includeGit {
			continue
		}
		args = append(args, "--exclude="+ex)
	}
	// Dry run: nothing is written to the target.
	args = append(args, c.sourcePath+"/", "/tmp/magento-staging-estimate-target/")
	out, runErr = run("rsync", args...)
	if runErr != nil {
		// rsync unavailable or failed — fall back to the full source size
		// (better to overestimate the disk needed than to fail).
		c.verbosef("rsync estimate failed: %v\n%s", runErr, out)
		return sourceTotal, sourceTotal, nil
	}
	if n, ok := parseRsyncTotalSize(out); ok {
		return sourceTotal, n, nil
	}
	c.verbosef("could not parse rsync --stats output; falling back to source size")
	return sourceTotal, sourceTotal, nil
}

// parseRsyncTotalSize extracts the "Total transferred file size" (bytes)
// from `rsync --stats` output, falling back to "Total file size" when no
// transfer would happen. Numbers may be formatted with thousands separators.
func parseRsyncTotalSize(out string) (int64, bool) {
	transferred := int64(-1)
	total := int64(-1)
	for _, line := range strings.Split(out, "\n") {
		if v, ok := parseRsyncSizeLine(line, "Total transferred file size:"); ok {
			transferred = v
		}
		if v, ok := parseRsyncSizeLine(line, "Total file size:"); ok {
			total = v
		}
	}
	if transferred >= 0 {
		return transferred, true
	}
	if total >= 0 {
		return total, true
	}
	return 0, false
}

// parseRsyncSizeLine parses one "Prefix: 1,234,567 bytes" style line,
// returning the byte count.
func parseRsyncSizeLine(line, prefix string) (int64, bool) {
	if !strings.HasPrefix(line, prefix) {
		return 0, false
	}
	var digits []byte
	for i := len(prefix); i < len(line); i++ {
		if line[i] >= '0' && line[i] <= '9' {
			digits = append(digits, line[i])
		}
	}
	if len(digits) == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(string(digits), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
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
//
//	+-----------------------------------------+
//	| IFNULL(SUM(data_length+index_length),0) |
//	+-----------------------------------------+
//	|                              3142369280 |
//	+-----------------------------------------+
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
	printHeader("Disk space estimate")
	fmt.Fprintln(os.Stderr)

	fmt.Fprintf(os.Stderr, "  %sFILES (httpdocs):%s\n", colorBold+colorCyan, colorReset)
	printKeyValue("Source (live)", humanSize(est.SourceFilesBytes))
	printKeyValue("Staging (with excludes)", humanSize(est.StagingFilesBytes))
	if est.FilesExcludedBytes > 0 {
		printInfo("%sExcluded%s (caches/logs/media/.git): %s  (%.1f%% reduction)",
			colorYellow, colorReset,
			humanSize(est.FilesExcludedBytes),
			100.0*float64(est.FilesExcludedBytes)/float64(est.SourceFilesBytes))
	}
	fmt.Fprintln(os.Stderr)

	fmt.Fprintf(os.Stderr, "  %sDATABASE:%s\n", colorBold+colorCyan, colorReset)
	printKeyValue("Source (live)", humanSize(est.SourceDBBytes))
	printKeyValue("Staging (schema-only empty)", humanSize(est.StagingDBBytes))
	if est.DBSchemaOnlyBytes > 0 {
		printInfo("%sSkipped data%s (%d schema-only tables): %s  (%.1f%% reduction)",
			colorYellow, colorReset,
			est.SchemaOnlyTableCount,
			humanSize(est.DBSchemaOnlyBytes),
			100.0*float64(est.DBSchemaOnlyBytes)/float64(est.SourceDBBytes))
	}
	fmt.Fprintln(os.Stderr)

	totalStaging := est.StagingFilesBytes + est.StagingDBBytes
	totalSource := est.SourceFilesBytes + est.SourceDBBytes
	pctStaging := 100.0 * float64(totalStaging) / float64(totalSource)
	fmt.Fprintf(os.Stderr, "  %sTOTAL STAGING footprint:%s %s  (vs source %s — %s%.1f%%%s of live)\n",
		colorBold+colorGreen, colorReset,
		bold(humanSize(totalStaging)),
		humanSize(totalSource),
		colorYellow, pctStaging, colorReset)
	fmt.Fprintln(os.Stderr)
}
