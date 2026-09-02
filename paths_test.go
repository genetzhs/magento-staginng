package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVhostAncestor(t *testing.T) {
	const vroot = "/var/www/vhosts"
	cases := []struct{ in, want string }{
		{"/var/www/vhosts/example.com/httpdocs", "/var/www/vhosts/example.com"},
		{"/var/www/vhosts/main.example.com/httpdocs", "/var/www/vhosts/main.example.com"},
		{"/var/www/vhosts/main.example.com/sub.example.com", "/var/www/vhosts/main.example.com"},
		{"/var/www/vhosts/main.example.com/secondary.com/httpdocs", "/var/www/vhosts/main.example.com"},
		{"/var/www/vhosts/example.com", "/var/www/vhosts/example.com"},
		{"/var/www/vhosts/example.com/", "/var/www/vhosts/example.com"},
		{"/var/www/vhosts", "/var/www/vhosts"},
		{"/srv/other/httpdocs", ""},
		{"/var/www/vhostsX/example.com", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := vhostAncestor(vroot, c.in); got != c.want {
			t.Errorf("vhostAncestor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDBFirstField(t *testing.T) {
	cases := []struct{ in, want string }{
		// Plain tab-separated rows
		{"/var/www/vhosts/example.com/httpdocs\n", "/var/www/vhosts/example.com/httpdocs"},
		{"www_root\n/var/www/vhosts/example.com/httpdocs\n", "/var/www/vhosts/example.com/httpdocs"},
		{"/var/www/vhosts/example.com/httpdocs\ttrue\t0\n", "/var/www/vhosts/example.com/httpdocs"},
		{"", ""},
		// mysql ASCII table output (plesk db)
		{"+-------------------+\n| www_root          |\n+-------------------+\n| /vhosts/example   |\n+-------------------+\n", "/vhosts/example"},
		{"+------+-------+\n| name | value |\n+------+-------+\n| a    | b     |\n+------+-------+\n", "a"},
		{"+------+-------+\n| name | value |\n+------+-------+\n| main | one   |\n| sub  | two   |\n+------+-------+\n", "sub"},
	}
	for _, c := range cases {
		if got := dbFirstField(c.in); got != c.want {
			t.Errorf("dbFirstField(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func writeTestEnvPHP(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "app", "etc"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "etc", "env.php"), []byte("<?php return [];"), 0640); err != nil {
		t.Fatal(err)
	}
}

func TestMagentoRootNear(t *testing.T) {
	// Store-root layout: Magento directly in the document root.
	root := t.TempDir()
	writeTestEnvPHP(t, root)
	if got := magentoRootNear(root); got != root {
		t.Errorf("magentoRootNear(store root) = %q, want %q", got, root)
	}

	// Recommended layout: document root is <magento root>/pub.
	pub := filepath.Join(root, "pub")
	if got := magentoRootNear(pub); got != root {
		t.Errorf("magentoRootNear(pub) = %q, want %q", got, root)
	}

	// Nothing Magento nearby.
	other := t.TempDir()
	if got := magentoRootNear(other); got != "" {
		t.Errorf("magentoRootNear(empty) = %q, want %q", got, "")
	}
	if got := magentoRootNear(""); got != "" {
		t.Errorf("magentoRootNear(\"\") = %q, want empty", got)
	}
}

func TestPhpBinFromHandlerID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plesk-php74-fpm", "/opt/plesk/php/7.4/bin/php"},
		{"plesk-php83-fastcgi", "/opt/plesk/php/8.3/bin/php"},
		{"plesk-php56-fpm", "/opt/plesk/php/5.6/bin/php"},
		{"plesk-php80-fpm", "/opt/plesk/php/8.0/bin/php"},
		{"", ""},
		{"cgi", ""},
		{"php", ""},
		{"phpx-fpm", ""},
	}
	for _, c := range cases {
		if got := phpBinFromHandlerID(c.in); got != c.want {
			t.Errorf("phpBinFromHandlerID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFindMagentoRoot(t *testing.T) {
	base := t.TempDir()

	// Custom document root directory (no httpdocs present).
	custom := filepath.Join(base, "www3.example.com")
	writeTestEnvPHP(t, custom)

	// A previous staging copy must be skipped even though it is a Magento root.
	stg := filepath.Join(base, "staging")
	writeTestEnvPHP(t, stg)

	c := &config{stagingName: "staging"}
	if got := findMagentoRoot(c, base); got != custom {
		t.Errorf("findMagentoRoot = %q, want %q", got, custom)
	}

	// Conventional httpdocs wins over the scan.
	conv := t.TempDir()
	writeTestEnvPHP(t, filepath.Join(conv, "httpdocs"))
	if got := findMagentoRoot(c, conv); got != filepath.Join(conv, "httpdocs") {
		t.Errorf("findMagentoRoot(httpdocs) = %q, want %q", got, filepath.Join(conv, "httpdocs"))
	}
}
