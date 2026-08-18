# magento-staging

Go binary that creates a Magento 2 staging site on a Plesk server.

The binary runs **on the Plesk server as root** and orchestrates the full
staging creation: Plesk subdomain, database copy (with schema-only tables for
speed), file rsync (excluding caches/logs/media-cache), env.php patching,
core_config_data updates (URLs, SMTP disable, NOINDEX, ES prefix change,
payments off, analytics off, cron off), Magento CLI setup, permissions fix and
HTTP Basic Auth.

## Install

### Option A: Download pre-built binary (recommended)

```bash
# Latest release
curl -sL https://github.com/genetzhs/magento-staginng/releases/latest/download/magento-staging-linux-amd64 -o magento-staging
chmod +x magento-staging

# Or pick a specific release + architecture
#   magento-staging-linux-amd64   (x86_64 Linux)
#   magento-staging-linux-arm64   (aarch64 Linux)
#   magento-staging-darwin-amd64  (Intel macOS)
#   magento-staging-darwin-arm64  (Apple Silicon macOS)
```

Verify the SHA256 against `checksums.txt` on the release page.

### Option B: Build from source (any Go >= 1.16)

```bash
git clone https://github.com/genetzhs/magento-staginng.git
cd magento-staginng
make build           # linux/amd64 only
make build-all       # all 4 platforms
make VERSION=v1.0.0  # embed version string
```

### Deploy to a Plesk server

```bash
scp bin/magento-staging-linux-amd64 root@server:/var/www/vhosts/<domain>/magento-staging
```

The binary lives in the webspace root next to `httpdocs/` (not web-accessible).

## Usage

```bash
# Interactive (auto-detects everything from env.php + Plesk)
ssh root@server
cd /var/www/vhosts/<domain>
./magento-staging create

# Non-interactive
./magento-staging create \
  --domain example.com \
  --staging-name staging \
  --non-interactive

# Dry run (no changes)
./magento-staging create --domain example.com --dry-run

# Show all stagings on this server
./magento-staging list

# Show staging details
./magento-staging info --domain example.com --staging-name staging

# Manual cleanup
./magento-staging cleanup --domain example.com --staging-name staging
```

## What it does

See `docs/architecture.md` for the full phase plan.

## Schema-only tables

Database tables whose **data is skipped** (only structure is copied) to speed
up the dump and avoid leaking production payment tokens / search history:

- Indexer changelogs (`*_cl`), replicas (`*_replica`), temp tables (`*_tmp`)
- Sales aggregates (`sales_bestsellers_aggregated_*`, `salesrule_coupon_aggregated_*`)
- Search history (`search_query`, `amasty_xsearch_users_search`)
- Logs (`cron_schedule`, `customer_log`, `customer_visitor`, `adminnotification_inbox`)
- Cache / sessions (`cache`, `cache_tag`, `session`)
- Security tokens (`oauth_token`, `oauth_nonce`, `vault_payment_token*`, `integration`)
- Payment transactions (`eurobank_transactions`, `paypal_payment_transaction`)
- Admin UI state (`ui_bookmark`, `admin_passwords`, `admin_user_session`)
- Customer grid flat (`customer_grid_flat`)
- Reporting (`reporting_*`, `report_viewed_*`)
- Queues (`queue`, `queue_lock`, `magento_bulk`, `magento_operation`)
- Import/export history (`amasty_import_process`, `amasty_export_process`)

Override with `--schema-only-file` (one table name per line) or
`--no-schema-only` to copy all data.

### GDPR-friendly staging (no customer / sales data)

```bash
./magento-staging create --domain example.com \
  --no-sales-data \
  --no-customer-data
```

- `--no-sales-data` — schema only for `sales_order*`, `sales_invoice*`,
  `sales_shipment*`, `sales_creditmemo*`, `quote*`, `salesrule*`,
  `sequence_*`, `inventory_reservation`, `inventory_source_item`,
  `amasty_order*`, `eurobank_transactions`, `paypal_billing_agreement*`,
  `gift_message`.
- `--no-customer-data` — schema only for `customer_entity*`,
  `customer_address_entity*`, `newsletter_subscriber`, `review*`, `rating*`,
  `wishlist*`, `email_*`, `oauth_token`, `vault_payment_token*`,
  `login_as_customer*`.

Combined effect on a typical Magento 2.4 store (~3 GB DB):

| Setup | Schema-only tables | Staging DB size | Reduction |
|-------|---------------------|------------------|-----------|
| Default | 138 | 2.11 GB | 27.7% |
| `--no-sales-data` + `--no-customer-data` | 264 | 1.62 GB | 44.5% |
