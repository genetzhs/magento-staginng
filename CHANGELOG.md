# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
