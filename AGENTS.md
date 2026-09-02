# AGENTS.md

Guidance for AI coding agents working on this repository.

## Project

`magento-staging` — Go CLI (single `package main`, no external deps) that
creates a Magento 2 staging copy on a Plesk server. It runs **as root on
the Plesk server**, never remotely.

## Build & verify

- **NEVER build binaries on the local dev machine** — the endpoint
  security agent (SentinelOne) may quarantine them. All binaries are
  produced by GitHub Actions (see Release process below).
- Safe local checks: `go vet ./...`, `gofmt -s -l .`, `go test ./...`
  (never leave compiled artifacts on disk).
- `make build` / `make build-all` exist for CI reference only; do not run
  them locally.

## Release process

1. Update `CHANGELOG.md` (new `[X.Y.Z] - date` section).
2. Commit to `main` and push.
3. `git tag vX.Y.Z && git push origin vX.Y.Z` — the `v*` tag triggers
   `.github/workflows/release.yml`, which cross-compiles all binaries,
   verifies them and publishes the GitHub Release. The version string
   embedded in the binary comes from the tag.
4. Deploy by downloading the release binary **directly on the target
   server** (`curl -sL .../releases/latest/download/magento-staging-linux-amd64`),
   never by scp-ing from the dev machine (nothing is built locally).

## Architecture notes

- **Path resolution** (`paths.go`): never assume
  `/var/www/vhosts/<domain>/httpdocs`. The document root, the webspace
  (subscription) root and the webspace name are resolved from Plesk
  (`plesk db` → `psa` tables): subscriptions can be renamed (the vhosts
  directory keeps its old name), a domain can be secondary inside another
  subscription, and document roots can be custom or point at `pub/`.
  Staging target, credentials and log file always live under the webspace
  root, not under `/var/www/vhosts/<domain>/`.
- Plesk CLI lives at `/usr/sbin/plesk`; Plesk multi-PHP in
  `/opt/plesk/php/*/bin`.
- `plesk db` output can be plain tab-separated rows or mysql ASCII
  tables — use the shared parsers (`dbFirstField`, `parsePLESKDBNumber`).

## Security rules

- No production identifiers (real domains, server hostnames, IPs) in
  source, tests, docs or comments — CI scans binaries when the
  `FORBIDDEN_IDENTIFIERS` repository variable is set. Use `example.com`
  style placeholders.
- The tool runs as root on managed servers — every command it executes
  must be deliberate; prefer Plesk CLI / read-only SQL over raw system
  changes.
- **Never run `create` against a production server without `--dry-run`**
  unless the user explicitly asks for the live run.

## Server-specific facts

Access details and per-domain layout facts for the managed servers live
in the agent's global memory (`~/.config/opencode/AGENTS.md`), not in
this repo.
