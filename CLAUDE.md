# Directory Connector — Locke AD Sync Agent

## Quick Reference

| Task | Command |
|------|---------|
| Local build | `make local` |
| Cross-compile all | `make build` |
| Test (unit) | `make test` |
| Run one-shot sync | `./dist/locke-connector sync` |
| Run daemon | `./dist/locke-connector run` |
| Dry run | `./dist/locke-connector sync --dry-run` |
| Show status | `./dist/locke-connector status` |

## Project Overview

Standalone Go binary that syncs users and groups from on-premises Active Directory to Locke via SCIM 2.0. Reads AD over LDAP (read-only), pushes changes to Locke's existing SCIM endpoints. No cloud intermediary.

**Spec doc:** `docs/features/DIRECTORY_CONNECTOR.md`

## Architecture

```
internal/config/     YAML + env var config loading
internal/ldap/       LDAP client (connect, search, paging, objectGUID, tombstones)
internal/scim/       HTTP SCIM client (CRUD Users, PATCH Groups, rate limiting, retries)
internal/sync/       Sync engine (incremental via uSNChanged, full reconciliation)
internal/state/      SQLite state store (known users, groups, high-water mark)
internal/service/    Windows Service / systemd wrappers (Phase 3)
cmd/                 Cobra CLI commands
```

## Key Patterns

- **Incremental sync:** Uses AD's `uSNChanged` monotonic counter as high-water mark. Only queries objects changed since last sync.
- **Full sync fallback:** Every 6 hours, does a complete reconciliation to catch edge cases (DC failover, manual edits).
- **objectGUID as join key:** Immutable across renames. Format (base64 vs UUID) must match ADFS SAML assertion output.
- **Rate limiting:** Proactive client-side throttle at 80 req/min (Locke limit is 100/min).
- **No CGO:** Pure-Go SQLite (`modernc.org/sqlite`) for clean cross-compilation.
- **Simple LDAP bind:** Service account authenticates via DN + password over TLS. No Kerberos, no NTLM.

## Testing

- Unit tests use in-memory SQLite for state store tests
- LDAP tests against mock server (glauth or gldap)
- SCIM client tests against `net/http/httptest` mock
- Integration tests mock both LDAP and SCIM to test full sync lifecycle

## Config

Copy `configs/example.yaml` to `./locke-connector.yaml`. Sensitive values via env vars:
- `LOCKE_SCIM_TOKEN` — SCIM bearer token
- `LDAP_BIND_PASSWORD` — service account password
- `LDAP_HOST` — domain controller hostname
