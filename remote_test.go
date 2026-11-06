package main

import (
	"os"
	"strings"
	"testing"
)

func TestSubscriptionLoginFor(t *testing.T) {
	cases := map[string]string{
		"shop.staging.example.net":     "shop",
		"my-shop.example.com":          "myshop",
		"1shop.example.com":            "stg1shop",
		"averyveryverylongname.ex.com": "averyveryverylon",
	}
	for in, want := range cases {
		if got := subscriptionLoginFor(in); got != want {
			t.Errorf("subscriptionLoginFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMysqlClientOptionFile(t *testing.T) {
	got := mysqlClientOptionFile("u", `p"w\x`, "db.example.com:3307")
	for _, want := range []string{`user="u"`, `"p\"w\\x"`, `host="db.example.com"`, "port=3307"} {
		if !strings.Contains(got, want) {
			t.Errorf("option file missing %q:\n%s", want, got)
		}
	}
	if s := mysqlClientOptionFile("u", "p", "/var/run/mysqld/mysqld.sock"); !strings.Contains(s, `socket="/var/run/mysqld/mysqld.sock"`) {
		t.Errorf("socket not handled:\n%s", s)
	}
}

func TestDBColumnValues(t *testing.T) {
	ascii := "+------------+\n| ip_address |\n+------------+\n| 192.0.2.1  |\n| 192.0.2.2  |\n+------------+\n"
	got := dbColumnValues(ascii, "ip_address")
	if len(got) != 2 || got[0] != "192.0.2.1" || got[1] != "192.0.2.2" {
		t.Errorf("ascii: got %v", got)
	}
	if got := dbColumnValues("192.0.2.1\n", "ip_address"); len(got) != 1 {
		t.Errorf("plain: got %v", got)
	}
}

func TestSSHArgsAndStagingHost(t *testing.T) {
	c := &config{remoteHost: "live.example.com", remoteUser: "root", remotePort: "2222", sshKey: "/k"}
	args := sshCommandArgs(c, nil, "true")
	if args[len(args)-2] != "--" || args[len(args)-3] != "root@live.example.com" {
		t.Errorf("unexpected ssh args %v", args)
	}
	c.stagingFQDN = "shop.staging.example.net"
	if c.stagingURL() != "https://shop.staging.example.net/" {
		t.Errorf("stagingURL = %s", c.stagingURL())
	}
	l := &config{stagingName: "staging", domain: "example.com"}
	if l.stagingHost() != "staging.example.com" {
		t.Errorf("stagingHost = %s", l.stagingHost())
	}
}

func TestReadAuthorizedKeys(t *testing.T) {
	dir := t.TempDir()
	good := dir + "/keys"
	_ = os.WriteFile(good, []byte("# team\nssh-ed25519 AAAAC3Nz dev@example.com\n\nssh-rsa AAAAB3Nz ops@example.com\n"), 0600)
	keys, err := readAuthorizedKeys(good)
	if err != nil || len(keys) != 2 {
		t.Fatalf("got %v, %v", keys, err)
	}
	bad := dir + "/bad"
	_ = os.WriteFile(bad, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n"), 0600)
	if _, err := readAuthorizedKeys(bad); err == nil {
		t.Error("private key line accepted")
	}
}
