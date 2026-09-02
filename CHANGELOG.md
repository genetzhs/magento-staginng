# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.7] - 2026-09-02

### Fixed
- PHP CLI binary detection now prefers the PHP handler Plesk serves for
  the live domain (`hosting.php_handler_id`, e.g. plesk-php74-fpm →
  /opt/plesk/php/7.4/bin/php) over simply picking the highest installed
  version. Old Magento releases (Composer 1.x in vendor/) fatal on PHP
  8.1+ ("During inheritance of Countable"), so staging CLI commands must
  run the production PHP version.
- The staging subdomain is now created with the live domain's PHP
  handler (`-php_handler_id`), so web requests match production too.

## [1.0.6] - 2026-09-02

### Fixed
- Database and DB user creation now passes the subscription's MAIN
  domain to `plesk bin database` (`-domain <webspace-name>`). Creating
  them with a secondary domain failed with "This object can be created
  only in a subscription."
- Database existence check: `plesk bin database --info` is not a valid
  command on all Plesk versions ("Unknown command: --info"); the check
  now uses `plesk db "SHOW DATABASES LIKE '<db>'"` with the old CLI form
  as fallback.

## [1.0.5] - 2026-09-02

### Fixed
- Disk space estimate for files: the staging size is now measured with
  `rsync -a -n --stats` using the exact exclude patterns of the real copy.
  Previously it used `du --exclude`, which never matched the leading-"/"
  anchored patterns (du matches "/"-patterns against the whole absolute
  path, rsync against paths relative to the transfer root) — so the
  estimate reported ~zero savings and over-reported the staging footprint
  by the size of var/log, var/cache, pub/static, .git, etc.

## [1.0.4] - 2026-09-02

### Changed
- Source path auto-detection: the document root is resolved from Plesk
  (`plesk db` → `psa.hosting.www_root`, with domain-alias fallback) instead
  of assuming `/var/www/vhosts/<domain>/httpdocs`. Handles custom document
  roots, renamed subscriptions (vhosts directory named after another
  domain), secondary domains and `pub/` document roots.
- Staging directory, credentials file and log file are now created under
  the actual subscription (webspace) root instead of
  `/var/www/vhosts/<domain>/`.
- Plesk subdomain creation uses the subscription name resolved from the
  Plesk database (fallback: the domain itself).
- `info` and `cleanup` resolve the credentials path via Plesk as well.

### Fixed
- `create` without `--domain` now prompts for the domain as documented
  (previously it aborted early with "--domain is required").

### Added
- Unit tests for the path resolution helpers (`paths_test.go`).

## [1.0.0] - 2026-08-18

### Added
- Initial implementation: Go binary that creates a Magento 2 staging site on a Plesk server.
- 12-phase orchestration with auto-rollback for early phases.
- Plesk CLI integration: subdomain creation (custom `-www-root`), database,
  database user, Let's Encrypt SSL, HTTP Basic Auth protected URL.
- env.php parser via PHP CLI round-trip (`php -r 'echo json_encode(...);'`)
  with auto-detection of the PHP binary (Plesk multi-PHP aware).
- mysqldump pipe (2-pass: schema for all tables + data for non-excluded).
- 138 base schema-only table patterns (caches, logs, tokens, queues, etc.).
- `--no-sales-data` flag: skip data for sales/quote/invoice/shipment tables.
- `--no-customer-data` flag: skip data for customer/newsletter/review/wishlist
  tables (GDPR-friendly staging).
- 31 rsync excludes (caches, logs, media cache, .git, connector, sitemaps).
- Disk space estimate (read-only) shown before any change.
- `core_config_data` updates: URLs, SMTP off, NOINDEX,NOFOLLOW, ES prefix
  change, cron off, payments off, analytics off.
- HTTP Basic Auth via Plesk `protected_url` with `.htaccess` fallback.
- Credentials stored at `/var/www/vhosts/<domain>/.credentials/<name>.json`
  with mode 0400.
- `list`, `info`, `cleanup` subcommands.
- `--check-update` via GitHub API.
- `--dry-run`, `--non-interactive`, `--verbose` flags.
- GitHub Actions: CI on push/PR, Release on tag (`v*`).
- Cross-compiled binaries: linux/amd64, linux/arm64, darwin/amd64,
  darwin/arm64 with SHA256 checksums.

### Security
- Binary contains no hardcoded credentials or production identifiers.
- Source DB password is read from `env.php` at runtime, never logged.
- All Plesk DB user passwords and HTTP Basic Auth passwords are generated
  randomly (24 and 16 chars respectively) by default.
- Credentials file is mode 0400 owned by root.
- Production identifiers (domains, server hostnames, IPs) are absent from
  source, tests, and binary (verified by CI when the
  `FORBIDDEN_IDENTIFIERS` repository variable is set).
