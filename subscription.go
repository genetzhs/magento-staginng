package main

import (
	"fmt"
	"net"
	"strings"
)

// dbColumnValues returns the first column of every data row of `plesk db`
// output (plain tab-separated or mysql ASCII table). The header row is
// dropped when it equals header.
func dbColumnValues(out, header string) []string {
	var vals []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "+") {
			continue
		}
		if strings.HasPrefix(line, "|") {
			line = strings.Trim(line, "| ")
		}
		field := line
		if i := strings.IndexAny(line, "\t|"); i >= 0 {
			field = line[:i]
		}
		field = strings.TrimSpace(field)
		if field == "" || field == header {
			continue
		}
		vals = append(vals, field)
	}
	return vals
}

// pleskIPv4Addresses lists the IPv4 addresses registered in this Plesk.
func pleskIPv4Addresses() ([]string, error) {
	out, err := run("/usr/sbin/plesk", "db", "SELECT ip_address FROM IP_Addresses")
	if err != nil {
		return nil, fmt.Errorf("plesk db failed: %v\n%s", err, out)
	}
	var ips []string
	for _, v := range dbColumnValues(out, "ip_address") {
		ip := net.ParseIP(v)
		if ip != nil && ip.To4() != nil && !ip.IsLoopback() {
			ips = append(ips, v)
		}
	}
	return ips, nil
}

// subscriptionLoginFor derives a Plesk system user name from a host name:
// first label, lowercase alphanumerics, starting with a letter, max 16.
func subscriptionLoginFor(host string) string {
	label := strings.ToLower(strings.SplitN(host, ".", 2)[0])
	var b strings.Builder
	for _, r := range label {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		s = "stg" + s
	}
	if len(s) > 16 {
		s = s[:16]
	}
	return s
}

// strongPassword returns a random password that also satisfies Plesk's
// password strength policy (upper, lower, digit and symbol present).
func strongPassword(length int) string {
	return generatePassword(length) + "Aa7#"
}

// pleskSiteExists reports whether a domain of any kind (subscription,
// addon domain, subdomain) with this name already exists.
func pleskSiteExists(name string) bool {
	_, err := run("/usr/sbin/plesk", "bin", "site", "--info", name)
	if err == nil {
		return true
	}
	_, err = run("/usr/sbin/plesk", "bin", "subscription", "--info", name)
	return err == nil
}

// pleskSubscriptionCreate creates the new subscription for the staging
// host on this server.
func pleskSubscriptionCreate(c *config) error {
	args := []string{
		"/usr/sbin/plesk", "bin", "subscription", "--create", c.stagingFQDN,
		"-owner", c.subOwner,
		"-service-plan", c.servicePlan,
		"-ip", c.targetIP,
		"-login", c.subLogin,
		"-passwd", c.subPass,
	}
	if c.dryRun {
		infof("  [dry-run] plesk bin subscription --create %s -owner %s -service-plan %q -ip %s -login %s -passwd ***",
			c.stagingFQDN, c.subOwner, c.servicePlan, c.targetIP, c.subLogin)
		return nil
	}
	out, err := run(args[0], args[1:]...)
	if err != nil {
		return fmt.Errorf("plesk subscription create failed: %v\n%s", err, out)
	}
	c.createdSub = true
	return nil
}

// pleskSiteUpdate runs `plesk bin site --update <host> <opts...>`.
func pleskSiteUpdate(c *config, opts ...string) error {
	if c.dryRun {
		infof("  [dry-run] plesk bin site --update %s %s", c.stagingFQDN, strings.Join(opts, " "))
		return nil
	}
	args := append([]string{"bin", "site", "--update", c.stagingFQDN}, opts...)
	out, err := run("/usr/sbin/plesk", args...)
	if err != nil {
		return fmt.Errorf("plesk site --update %s failed: %v\n%s", strings.Join(opts, " "), err, out)
	}
	return nil
}

// pleskSubscriptionRemove removes a subscription (files, databases, users).
func pleskSubscriptionRemove(name string) error {
	out, err := run("/usr/sbin/plesk", "bin", "subscription", "--remove", name)
	if err != nil {
		return fmt.Errorf("plesk subscription remove failed: %v\n%s", err, out)
	}
	return nil
}

// diskAvailable returns free bytes on the filesystem holding path.
func diskAvailable(path string) int64 {
	out, err := run("df", "-B1", "--output=avail", path)
	if err != nil {
		return -1
	}
	lines := strings.Fields(out)
	if len(lines) == 0 {
		return -1
	}
	return parsePLESKDBNumber(lines[len(lines)-1])
}
