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

## Migrate from another server (`migrate`)

`migrate` copies a live site from **another** Plesk server into a new
staging subscription. It always runs **on the destination server** (as
root) and connects to the source (live) server over SSH. The source is
only read: SELECTs on `psa`, `du`, `rsync`, `php -r` to parse env.php and
`mysqldump`. Nothing is written there.

```bash
# 1. Always start with a dry run
./magento-staging migrate \
    --source-host live.example.com --ssh-port 2222 \
    --domain example.com \
    --staging-domain example.staging.example.net \
    --dry-run

# 2. Real run (interactive confirmation; add --non-interactive for scripts)
./magento-staging migrate \
    --source-host live.example.com --ssh-port 2222 \
    --domain example.com \
    --staging-domain example.staging.example.net
```

### Requirements

- **SSH key auth** from the destination to the source. The default key is
  `/root/.ssh/migration_rsa`. Use `--ssh-key` to point to another key.
  Its public key must be in the source's `/root/.ssh/authorized_keys`.
  Connections use `BatchMode=yes`, so a rejected key fails immediately
  instead of asking for a password.
- **root on the source** (`--ssh-user`, default `root`), because the
  source is located with `plesk db`.
- **The source is a Plesk server.** The document root and PHP handler of
  `--domain` are read from its `psa` database. If they can't be resolved,
  pass `--source-path`.
- **DNS:** `--staging-domain` must point to the destination before the
  Let's Encrypt certificate can be issued. If that fails, the run
  continues and you can retry the certificate later.
- **Same services on the destination:** env.php is copied as is, apart
  from the DB credentials and the Redis prefix. The Redis and
  Elasticsearch/OpenSearch hosts it references must therefore be
  reachable from the destination. The tool prints the Redis hosts it
  finds.

> **Hostname gotcha:** if the live domain also exists on the destination
> Plesk (for example from an earlier migration), the destination's local
> DNS can resolve the source hostname to **the destination itself**. You
> then get `Permission denied (publickey)` even though the key is
> installed. Check with `getent hosts <source-host>` on the destination.
> If it points to the destination, pass the source **IP** to
> `--source-host`.

### What it does

1. Checks SSH and runs `plesk version` on the source.
2. Locates Magento on the source: Plesk document root → Magento root (at
   the docroot or its parent, so `pub/` docroots work), PHP handler.
3. Reads the source env.php over SSH: DB name, user and password, Redis
   prefix and admin path.
4. Estimates the size on the source (`du` + `rsync -n --stats`), shows
   the free space on the destination, and asks for confirmation.
5. Creates a **new subscription** `--staging-domain` with
   `plesk bin subscription --create`. Defaults: plan `Default Domain`,
   owner `admin`, the server's only IPv4 address, and a system user taken
   from the first label of the domain. Override with `--service-plan`,
   `--owner`, `--ip` and `--sys-user`. The system password is generated.
   The subscription gets the **same PHP handler** as the live site
   (warning if that handler is not installed on the destination).
6. Creates the database and DB user (`<source-db>stg` / `<source-user>stg`).
7. Empties `httpdocs/` (Plesk's default page), then runs `rsync -az` over
   SSH from the source using the [file excludes](#file-excludes).
8. Sets the document root to `httpdocs/pub`.
9. Copies the DB with `mysqldump` (schema for all tables + data for
   non-schema-only tables) over SSH, through `sed` (strips DEFINER), into
   the local `mysql`. The source DB password goes into a temporary option
   file (`umask 077`, deleted on exit) via the stdin of `ssh … bash -s`,
   so it never appears in a process list.
10. Same steps as `create`: env.php patch, `core_config_data` (URLs,
    cookie domain, SMTP off, NOINDEX, ES prefix, cron/payments/analytics
    off), Magento CLI (setup:upgrade, di:compile, static deploy, cache,
    reindex), permissions, Let's Encrypt, HTTP Basic Auth, verification.
11. Credentials (including the subscription's system user and password)
    go to `/var/www/vhosts/<staging-domain>/.credentials/staging.json`
    (0400).

**Rollback:** if something fails between creating the subscription and
copying the DB, the subscription is removed (along with its files, DB
and users). After that point it is left in place for debugging.

**Removal:** `magento-staging cleanup --domain <staging-domain>` deletes
the whole subscription.

### Migrate flags

| Flag | Default | Description |
|------|---------|-------------|
| `--source-host` | — | IP/hostname of the source (live) server |
| `--ssh-port` | `22` | SSH port of the source |
| `--ssh-user` | `root` | SSH user on the source |
| `--ssh-key` | `/root/.ssh/migration_rsa` | Private key |
| `--domain` | — | Live domain on the source |
| `--staging-domain` | — | FQDN of the new subscription on the destination |
| `--source-path` | from Plesk | Magento root on the source |
| `--service-plan` | `Default Domain` | Plesk service plan |
| `--owner` | `admin` | Owner of the subscription |
| `--ip` | the only IPv4 | IP of the subscription |
| `--sys-user` | first label of the domain | System user of the subscription |
| `--enable-ssh` | off | Gives the subscription's system user SSH access (`/bin/bash`) |
| `--ssh-authorized-keys` | — | File of public keys (one per line, `#` comments allowed) appended to `<webspace>/.ssh/authorized_keys` (0600, owner the system user). Implies `--enable-ssh` |

Also accepted, same as in `create`: `--target-db*`, `--redis-id-prefix`,
`--elastic-suffix`, `--basic-auth-*`, `--php-bin` (CLI on the
destination), `--magento-mode`, `--schema-only-file`, `--no-schema-only`,
`--no-sales-data`, `--no-customer-data`, `--include-git`, `--skip-*`,
`--dry-run`, `--non-interactive`, `--verbose`, `--log-file`.

## File excludes

Both `create` and `migrate` copy the Magento root with `rsync`, but
**skip** regenerable or disposable content. All patterns are anchored to
the Magento root (leading `/`), so they never match directories with the
same name inside `vendor/`. The directories themselves are kept (empty);
only their contents are excluded.

| Area | Excluded | Why |
|------|----------|-----|
| `var/` | `cache`, `page_cache`, `session`, `log`, `report`, `tmp`, `export`, `import`, `importexport`, `import_history`, `feeder`, `website_cache`, `view_preprocessed`, `composer_home`, `backups` | caches, sessions, logs, backups — rebuilt or not needed |
| `generated/` | all contents | rebuilt by `setup:di:compile` |
| `pub/static/` | `frontend/`, `adminhtml/`, `_requirejs/`, `deployed_version.txt` (`.htaccess` kept) | rebuilt by `setup:static-content:deploy` |
| `pub/media/` | `cache/`, `tmp/`, `analytics/`, `import/`, `.thumbs*/`, `catalog/product/cache/`, `catalog/product/cache.old/` | image caches/thumbnails, rebuilt on demand. **Original images are copied.** |
| `pub/` | `sitemap.xml`, `sitemap-*.xml` | rebuilt by cron |
| root | `connector/` (Plesk/Imunify), `nohup.out`, `output.log`, `error_log` | logs / server-specific |
| root | `.git/` | large history; include with `--include-git` |

The canonical list lives in `rsyncExcludes` in `defaults.go`. The size
estimate (`rsync -n --stats`) uses exactly the same patterns.

## Path auto-detection

All paths are resolved by asking Plesk for the **document root of the live
domain** (`plesk db` → `psa` `hosting.www_root`) instead of assuming
`/var/www/vhosts/<domain>/httpdocs`:

- **Source** — the Magento root at (or above) the Plesk document root.
  `.../pub` document roots (recommended Magento 2 layout) are handled.
- **Staging target** — `<webspace-root>/<staging-name>/`
- **Credentials & log** — `<webspace-root>/.credentials/`

The webspace root is the directory under `/var/www/vhosts/` that actually
contains the domain — e.g. `/var/www/vhosts/<main-domain>/` for renamed
subscriptions (the vhosts directory keeps its old name) or for secondary
domains. Use `--source-path` / `--target-path` to override.

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
