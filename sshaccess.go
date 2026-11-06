package main

import (
	"fmt"
	"os"
	"strings"
)

// sshKeyPrefixes are the key types accepted in an authorized_keys file
// passed with --ssh-authorized-keys.
var sshKeyPrefixes = []string{
	"ssh-rsa ", "ssh-ed25519 ", "ssh-dss ", "ecdsa-sha2-", "sk-ssh-ed25519@", "sk-ecdsa-sha2-",
}

// readAuthorizedKeys loads public keys from a file: one key per line,
// blank lines and # comments ignored. Lines that do not look like public
// keys (e.g. a private key pasted by mistake) are rejected.
func readAuthorizedKeys(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %v", path, err)
	}
	var keys []string
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ok := false
		for _, p := range sshKeyPrefixes {
			if strings.HasPrefix(line, p) {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("%s line %d is not an SSH public key", path, i+1)
		}
		keys = append(keys, line)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no public keys found in %s", path)
	}
	return keys, nil
}

// enableSubscriptionSSH gives the staging subscription's system user a
// /bin/bash shell and installs the public keys in
// <webspace-root>/.ssh/authorized_keys (the user's home directory).
// Existing keys in that file are kept; duplicates are not added twice.
func enableSubscriptionSSH(c *config) error {
	if c.dryRun {
		infof("  [dry-run] plesk bin subscription --update %s -shell /bin/bash", c.stagingFQDN)
		if len(c.sshAuthKeys) > 0 {
			infof("  [dry-run] install %d key(s) into %s/.ssh/authorized_keys", len(c.sshAuthKeys), c.webspaceRoot)
		}
		return nil
	}
	out, err := run("/usr/sbin/plesk", "bin", "subscription", "--update", c.stagingFQDN, "-shell", "/bin/bash")
	if err != nil {
		return fmt.Errorf("enabling shell for %s failed: %v\n%s", c.subLogin, err, out)
	}
	if len(c.sshAuthKeys) == 0 {
		return nil
	}

	dir := c.webspaceRoot + "/.ssh"
	file := dir + "/authorized_keys"
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	existing := ""
	if b, err := os.ReadFile(file); err == nil {
		existing = string(b)
	}
	content := existing
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	added := 0
	for _, k := range c.sshAuthKeys {
		if strings.Contains(existing, k) {
			continue
		}
		content += k + "\n"
		added++
	}
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0700)
	_ = os.Chmod(file, 0600)
	if out, err := run("chown", "-R", c.subLogin+":psacln", dir); err != nil {
		return fmt.Errorf("chown %s failed: %v\n%s", dir, err, out)
	}
	printOK("%d SSH key(s) installed in %s", added, file)
	return nil
}
