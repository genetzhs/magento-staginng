package main

import "testing"

const rsyncStatsSample = `Number of files: 171,712 (reg: 135,275, dir: 36,417, link: 20)
Number of created files: 0
Number of deleted files: 0
Number of regular files transferred: 135,275
Total file size: 4,601,888,570 bytes
Total transferred file size: 4,601,887,807 bytes
Literal data: 4,601,887,807 bytes
Matched data: 0 bytes
File list size: 4,770,602
File list generation time: 0.001 seconds
`

func TestParseRsyncTotalSize(t *testing.T) {
	got, ok := parseRsyncTotalSize(rsyncStatsSample)
	if !ok {
		t.Fatal("parseRsyncTotalSize failed on a valid --stats sample")
	}
	if got != 4601887807 {
		t.Errorf("parseRsyncTotalSize = %d, want 4601887807 (transferred size)", got)
	}

	// No transfer would happen: fall back to "Total file size".
	noTransfer := "Total file size: 1,234,567 bytes\nTotal transferred file size: 0 bytes\n"
	got, ok = parseRsyncTotalSize(noTransfer)
	if !ok || got != 0 {
		t.Errorf("parseRsyncTotalSize(noTransfer) = %d, %v; want 0, true", got, ok)
	}

	onlyTotal := "Total file size: 1,234,567 bytes\n"
	got, ok = parseRsyncTotalSize(onlyTotal)
	if !ok || got != 1234567 {
		t.Errorf("parseRsyncTotalSize(onlyTotal) = %d, %v; want 1234567, true", got, ok)
	}

	if _, ok := parseRsyncTotalSize(""); ok {
		t.Error("parseRsyncTotalSize(\"\") should not be ok")
	}
}

func TestParseRsyncSizeLine(t *testing.T) {
	if n, ok := parseRsyncSizeLine("Total file size: 4,601,888,570 bytes", "Total file size:"); !ok || n != 4601888570 {
		t.Errorf("parseRsyncSizeLine = %d, %v; want 4601888570, true", n, ok)
	}
	if n, ok := parseRsyncSizeLine("Total file size: 42", "Total file size:"); !ok || n != 42 {
		t.Errorf("parseRsyncSizeLine plain = %d, %v; want 42, true", n, ok)
	}
	if _, ok := parseRsyncSizeLine("Total literal data: 99 bytes", "Total file size:"); ok {
		t.Error("prefix must not match other stat lines")
	}
	if _, ok := parseRsyncSizeLine("Total file size: N/A", "Total file size:"); ok {
		t.Error("no digits should not be ok")
	}
}
