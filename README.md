# Locke Directory Connector

A lightweight agent that syncs users and groups from on-premises Active Directory to Locke via SCIM 2.0.

## Why This Exists

Enterprise customers using on-premises AD today have only one path to automated provisioning:

1. Install Azure AD Connect to sync to Entra ID
2. Configure an Enterprise Application with SCIM provisioning
3. Pay for Entra ID P1 licensing (~$6/user/month)

That's too much friction for a security product targeting enterprises who specifically want to minimize cloud dependencies. The Directory Connector eliminates the intermediary — it reads AD directly over LDAP and pushes user lifecycle events to Locke's SCIM endpoints. No cloud service in the middle, no extra licensing, 10-minute setup.

## What It Does

- **Creates** Locke accounts when users appear in AD
- **Updates** names/emails when AD attributes change
- **Disables** Locke accounts when users are disabled in AD
- **Re-enables** accounts when users are re-enabled
- **Deletes** Locke accounts when users are removed from AD (tombstone detection)
- **Syncs group membership** to Locke vaults (Phase 4)

Sync is incremental — it uses AD's `uSNChanged` counter to only query objects that changed since last sync. A full reconciliation runs every 6 hours as a consistency check.

## How It Works

```
┌─────────────────────────────────┐
│         Customer Network        │
│                                 │
│  ┌───────────────┐              │
│  │  AD Domain    │              │
│  │  Controllers  │              │
│  └───────┬───────┘              │
│          │ LDAPS (read-only)    │
│          ▼                      │
│  ┌───────────────────────────┐  │           ┌─────────────────┐
│  │  Locke Directory          │  │  HTTPS    │                 │
│  │  Connector (Go binary)    │──┼──────────▶│  Locke API      │
│  │                           │  │  SCIM 2.0 │  /scim/v2/*     │
│  │  ┌─────────┐ ┌─────────┐ │  │           │                 │
│  │  │ Syncer  │ │ SQLite  │ │  │           └─────────────────┘
│  │  │ Engine  │ │ State   │ │  │
│  │  └─────────┘ └─────────┘ │  │
│  └───────────────────────────┘  │
│                                 │
└─────────────────────────────────┘
```

The connector authenticates to AD via LDAP simple bind (service account DN + password over TLS). It never writes to AD — the service account has read-only access to specific OUs only.

## Project Structure

```
directory-connector/
├── main.go                   # Entry point
├── cmd/                      # Cobra CLI commands
│   ├── root.go               # Root command + config init
│   ├── sync.go               # One-shot sync (--full, --dry-run)
│   ├── run.go                # Daemon mode (ticker-based)
│   └── status.go             # Show sync status
├── internal/
│   ├── config/               # YAML config + env var loading + validation
│   ├── ldap/                 # LDAP client (TLS, paging, objectGUID, tombstones)
│   ├── scim/                 # HTTP SCIM client (CRUD, retries, rate limiting)
│   ├── sync/                 # Sync engine (incremental + full reconciliation)
│   ├── state/                # SQLite state store (users, groups, high-water mark)
│   └── service/              # Windows Service / systemd wrappers (TODO)
├── configs/
│   └── example.yaml          # Annotated example config
├── tests/                    # Unit + integration tests
├── Makefile                  # Cross-compile targets
└── CLAUDE.md                 # Dev quick reference
```

## Quick Start

```bash
# Build for your platform
make local

# Copy and fill in config
cp configs/example.yaml ./locke-connector.yaml

# Set secrets via env vars
export LDAP_BIND_PASSWORD="your-service-account-password"
export LOCKE_SCIM_TOKEN="locke_scim_your-org_xxxxx"

# Test connection (shows what would happen, no changes)
./dist/locke-connector sync --dry-run

# Run one-shot full sync
./dist/locke-connector sync --full

# Run as daemon (syncs every 5 minutes)
./dist/locke-connector run
```

## Deployment

### Windows Server (most common for AD environments)

```powershell
# Download the Windows binary
# Place locke-connector.exe and locke-connector.yaml in C:\Program Files\Locke\

# Set environment variables (or use Windows Credential Manager)
[System.Environment]::SetEnvironmentVariable("LDAP_BIND_PASSWORD", "...", "Machine")
[System.Environment]::SetEnvironmentVariable("LOCKE_SCIM_TOKEN", "...", "Machine")

# Install and start as Windows Service (Phase 3 — not yet implemented)
locke-connector.exe service install
locke-connector.exe service start
```

### Linux (systemd)

```bash
# Place binary in /usr/local/bin/, config in /etc/locke-connector/
sudo cp locke-connector /usr/local/bin/
sudo mkdir -p /etc/locke-connector
sudo cp locke-connector.yaml /etc/locke-connector/

# Install and start as systemd unit (Phase 3 — not yet implemented)
locke-connector service install --systemd
sudo systemctl enable locke-connector
sudo systemctl start locke-connector
```

### Docker

```bash
# Phase 3 — Dockerfile not yet created
docker run -v ./locke-connector.yaml:/etc/locke-connector/locke-connector.yaml \
  -e LDAP_BIND_PASSWORD=... \
  -e LOCKE_SCIM_TOKEN=... \
  locke/directory-connector:latest run
```

## AD Service Account Setup

The connector needs a **read-only** service account with minimal permissions. See the full setup guide in `docs/features/DIRECTORY_CONNECTOR.md` → "Service Account — Minimum Required Permissions".

Summary:
- Create a dedicated service account (e.g., `locke-sync`)
- Delegate **read-only** access to user/group OUs only (not domain-wide)
- Optionally grant read access to `CN=Deleted Objects` for real-time delete detection
- Deny interactive logon (defense in depth)
- The account cannot write to AD, read passwords, or access anything outside delegated OUs

## Security Model

- **LDAP:** Read-only service account, TLS mandatory (LDAPS or StartTLS)
- **SCIM token:** Only secret stored — encrypted at rest or supplied via env var
- **No passwords transit:** Only user metadata (names, emails, group memberships, objectGUID)
- **objectGUID as join key:** Immutable identifier — survives username renames

## Configuration

See `configs/example.yaml` for the full annotated config. Key settings:

| Setting | Default | Description |
|---------|---------|-------------|
| `ldap.port` | 636 | 636 for LDAPS, 389 for StartTLS |
| `sync.interval` | 5m | How often to check for changes |
| `sync.full_sync_interval` | 6h | How often to do full reconciliation |
| `mapping.user_id_format` | base64 | Must match ADFS SAML assertion format |

## Current Status

**Phase 1 complete.** The core sync engine works: LDAP queries, incremental sync via uSNChanged, full reconciliation, SCIM CRUD with retries/rate-limiting, and SQLite state tracking. Cross-compiles cleanly to Windows.

**Next steps:**
- Phase 2: `configure` interactive wizard, log rotation, LDAP reconnection
- Phase 3: Windows Service wrapper, systemd unit, Docker image, GitHub Actions release
- Phase 4: Group membership sync, group filters, rename handling
