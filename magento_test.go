package main

import "testing"

func TestUnsupportedRDBMSVersion(t *testing.T) {
	out := "Schema creation/updates:\nCurrent version of RDBMS is not supported. Used Version: 10.11.19-MariaDB. Supported versions: MySQL-8, MySQL-5.7, MariaDB-(10.2-10.6)\n"
	got, ok := unsupportedRDBMSVersion(out)
	if !ok || got != "MariaDB-10.11" {
		t.Fatalf("got %q %v", got, ok)
	}
	if _, ok := unsupportedRDBMSVersion("all good"); ok {
		t.Fatal("false positive")
	}
}

func TestVersionAllowed(t *testing.T) {
	di := `<type name="Magento\Framework\DB\Adapter\SqlVersionProvider">
        <arguments>
            <argument name="supportedVersionPatterns" xsi:type="array">
                <item name="MySQL-8" xsi:type="string">^8\.0\.</item>
                <item name="MariaDB-(10.2-10.6)" xsi:type="string">^10\.[2-6]\.</item>
            </argument>
        </arguments>
    </type>`
	if ok, _ := versionAllowed(di, "10.6.18"); !ok {
		t.Fatal("10.6 should be allowed")
	}
	if ok, _ := versionAllowed(di, "10.11.19"); ok {
		t.Fatal("10.11 should not be allowed")
	}
	if l := rdbmsLabel("10.11.19", "MariaDB"); l != "MariaDB-10.11" {
		t.Fatalf("label %q", l)
	}
}
