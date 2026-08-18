package main

import (
	"fmt"
	"os"
)

const helpText = `magento-staging %s (commit %s)

Create a Magento 2 staging site on a Plesk server.

USAGE:
    magento-staging <command> [flags]

COMMANDS:
    create    Create a new staging site (interactive by default)
    list      List all staging sites on this server
    info      Show details for a staging site
    cleanup   Delete a staging site (interactive confirmation)
    version   Show version

CREATE FLAGS:
    --domain <domain>              Target domain (e.g. example.com). Required in
                                   non-interactive mode; prompted otherwise.
    --staging-name <name>          Staging subdomain prefix (default: staging)
    --source-path <path>           Source httpdocs path
                                   (default: /var/www/vhosts/<domain>/httpdocs)
    --target-path <path>           Target staging path
                                   (default: /var/www/vhosts/<domain>/<staging-name>)
    --source-db <db>               Source database (default: auto-detect from env.php)
    --target-db <db>               Target database name (default: <source-db>stg)
    --target-db-user <user>        Target DB user (default: <source-db-user>stg)
    --target-db-pass <pass>        Target DB password (default: random 24 chars)
    --redis-id-prefix <prefix>     Redis cache id_prefix (default: <orig>stg_)
    --elastic-suffix <suffix>     Elasticsearch index suffix (default: stg)
    --basic-auth-user <user>       HTTP Basic Auth user (default: admin)
    --basic-auth-pass <pass>       HTTP Basic Auth password (default: random 16 chars)
    --php-bin <path>               PHP CLI binary path
                                   (default: auto-detect from env.php)
    --magento-mode <mode>          Set MAGE_MODE: developer|production
                                   (default: keep source mode)
    --schema-only-file <path>     File with table names to copy schema-only
                                   (one per line; overrides defaults)
    --no-schema-only               Copy full data for all tables
    --include-git                  Include .git/ directory in rsync
    --skip-ssl                     Skip Let's Encrypt issuance
    --skip-basic-auth              Skip HTTP Basic Auth
    --skip-payment-disable         Do not disable payment methods
    --skip-analytics-disable       Do not disable analytics (GA/Pixel/GTM)
    --skip-cron-disable            Do not set dev/cron/disable=1
    --skip-email-disable           Do not set system/smtp/disable=1
    --skip-seo-disable             Do not set NOINDEX,NOFOLLOW
    --no-sales-data                Skip data for sales/quote/invoice/shipment
                                   tables (schema only) - good for staging
                                   without production orders (~430 MB on avg)
    --no-customer-data             Skip data for customer/newsletter/review/
                                   wishlist tables (schema only) - good for
                                   GDPR-friendly staging without customer PII
    --dry-run                      Preview only, no changes
    --non-interactive              Fail if any required flag is missing
    --verbose                      Verbose output (commands printed)
    --version                      Show version and exit
    --check-update                 Check GitHub for newer release
    --help, -h                     Show this help

EXAMPLES:
    # Interactive create
    ssh root@server
    cd /var/www/vhosts/example.com
    ./magento-staging create

    # Non-interactive
    ./magento-staging create --domain example.com --non-interactive

    # Dry run
    ./magento-staging create --domain example.com --dry-run

    # Custom staging name
    ./magento-staging create --domain example.com --staging-name qa

NOTES:
    * Runs as root on the Plesk server (do NOT run remotely).
    * The binary should live in the webspace root
      (e.g. /var/www/vhosts/<domain>/magento-staging) — not web-accessible.
    * Staging target path is /var/www/vhosts/<domain>/<staging-name>/
      (Magento files live directly in this directory, which is also the
      Plesk subdomain document root).
    * Target DB name uses suffix 'stg' (Plesk requires unique DB names per
      server; we avoid the underscore character because Plesk disallows it).
    * Credentials are stored at
      /var/www/vhosts/<domain>/.credentials/<staging-name>.json (chmod 0400).
`

func showHelp() {
	fmt.Fprintf(os.Stdout, helpText, version, commit)
	os.Exit(0)
}

func showVersion() {
	fmt.Printf("magento-staging %s (commit %s)\n", version, commit)
	os.Exit(0)
}
