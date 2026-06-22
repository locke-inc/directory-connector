# Locke Directory Connector

A lightweight agent that syncs users and groups from on-premises Active Directory to Locke via SCIM 2.0, and relays LDAP authentication challenges for pass-through login.

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
- **Syncs group membership** to Locke vaults
- **Relays LDAP auth** — users log in to Locke with their AD password (no separate credential)

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
│  │                           │◀─┼───────────│  (SSE stream)   │
│  │  ┌─────────┐ ┌─────────┐ │  │           │                 │
│  │  │ Syncer  │ │Auth     │ │  │           └─────────────────┘
│  │  │ Engine  │ │Relay    │ │  │
│  │  └─────────┘ └─────────┘ │  │
│  │  ┌─────────┐             │  │
│  │  │ SQLite  │             │  │
│  │  │ State   │             │  │
│  │  └─────────┘             │  │
│  └───────────────────────────┘  │
│                                 │
└─────────────────────────────────┘
```

**Sync:** The connector reads AD via LDAPS and pushes user/group changes to Locke's SCIM endpoints.

**Auth relay:** The connector maintains an outbound SSE connection to Locke's API. When a user logs in with their AD password, the API sends an auth challenge over the stream. The connector performs an LDAP bind against AD and posts the result back. No inbound firewall rules required.

## Project Structure

```
directory-connector/
├── main.go                   # Entry point
├── cmd/                      # Cobra CLI commands
│   ├── root.go               # Root command + config init
│   ├── sync.go               # One-shot sync (--full, --dry-run)
│   ├── run.go                # Daemon mode (ticker-based + auth relay)
│   ├── service.go            # Service install/uninstall/start/stop
│   └── status.go             # Show sync status
├── internal/
│   ├── config/               # YAML config + env var loading + validation
│   ├── ldap/                 # LDAP client (TLS, paging, objectGUID, tombstones)
│   ├── scim/                 # HTTP SCIM client (CRUD, retries, rate limiting)
│   ├── sync/                 # Sync engine (incremental + full reconciliation)
│   ├── relay/                # SSE auth relay client + LDAP bind handler
│   ├── state/                # SQLite state store (users, groups, high-water mark)
│   └── service/              # Windows Service / systemd wrappers
├── configs/
│   └── example.yaml          # Annotated example config
├── scripts/
│   └── Install-LockeConnector.ps1  # Automated Windows deployment
├── tests/                    # Unit + integration tests
├── Makefile                  # Cross-compile targets
└── CLAUDE.md                 # Dev quick reference
```

## Deployment (Windows Server)

### Automated Install (Recommended)

The PowerShell install script handles the entire setup: AD objects, binary, config, env vars, service install, and validation.

Download the binary and script from the [latest release](https://github.com/locke-inc/directory-connector/releases), then run:

```powershell
# Full install — creates AD service account, security group, configures everything
.\Install-LockeConnector.ps1 `
  -ScimToken "locke_scim_yourorg_xxxxxxxxxxxxxxxx" `
  -LdapPassword "service-account-password" `
  -DomainController "dc01.corp.example.com" `
  -BaseDN "DC=corp,DC=example,DC=com" `
  -GroupName "LockeUsers" `
  -GroupOU "OU=Groups,DC=corp,DC=example,DC=com" `
  -ServiceAccountOU "OU=Service Accounts,DC=corp,DC=example,DC=com"
```

The script will:
1. Create the AD service account with read-only permissions
2. Create the LockeUsers security group
3. Install the binary to `C:\Program Files\Locke\`
4. Generate the YAML config in `C:\ProgramData\Locke\`
5. Set environment variables (system scope + service registry)
6. Run a dry-run to validate connectivity
7. Install and start the Windows Service

**If AD objects already exist** (service account + group created manually):

```powershell
.\Install-LockeConnector.ps1 `
  -ScimToken "locke_scim_yourorg_xxxxxxxxxxxxxxxx" `
  -LdapPassword "service-account-password" `
  -DomainController "dc01.corp.example.com" `
  -BaseDN "DC=corp,DC=example,DC=com" `
  -SkipADSetup
```

**If the DC uses a self-signed certificate:**

```powershell
.\Install-LockeConnector.ps1 ... -SkipTLSVerify
```

### Manual Install

If you prefer to set things up step by step:

#### 1. Prerequisites (AD Admin)

**Create a service account:**
```powershell
New-ADUser -Name "svc-locke-connector" `
  -SamAccountName "svc-locke-connector" `
  -UserPrincipalName "svc-locke-connector@corp.example.com" `
  -AccountPassword (Read-Host -AsSecureString "Password") `
  -Enabled $true `
  -PasswordNeverExpires $true `
  -Path "OU=Service Accounts,DC=corp,DC=example,DC=com" `
  -Description "Locke Directory Connector (read-only AD sync)"
```

**Create a security group** (NOT an OU — must be a group for `memberOf` queries):
```powershell
New-ADGroup -Name "LockeUsers" `
  -GroupScope Global `
  -GroupCategory Security `
  -Path "OU=Groups,DC=corp,DC=example,DC=com" `
  -Description "Users synced to Locke Identity"
```

**Add users to the group:**
```powershell
# Individual
Add-ADGroupMember -Identity "LockeUsers" -Members "jsmith", "mwilson"

# Bulk — all users in an OU
Get-ADUser -SearchBase "OU=Staff,DC=corp,DC=example,DC=com" -Filter * |
  ForEach-Object { Add-ADGroupMember -Identity "LockeUsers" -Members $_ }
```

**Get the Distinguished Names** (needed for config):
```powershell
Get-ADUser svc-locke-connector | Select-Object DistinguishedName
Get-ADGroup LockeUsers | Select-Object DistinguishedName
```

#### 2. Install Binary + Config

```powershell
# Create directories
New-Item -ItemType Directory -Path "C:\Program Files\Locke" -Force
New-Item -ItemType Directory -Path "C:\ProgramData\Locke" -Force

# Copy binary
Copy-Item .\locke-connector-windows-amd64.exe "C:\Program Files\Locke\locke-connector.exe"

# Copy config (edit with your DNs first)
Copy-Item .\locke-connector.yaml "C:\ProgramData\Locke\locke-connector.yaml"
```

#### 3. Set Environment Variables

```powershell
[System.Environment]::SetEnvironmentVariable("LOCKE_SCIM_TOKEN", "locke_scim_yourorg_xxxxxxxxxxxxxxxx", "Machine")
[System.Environment]::SetEnvironmentVariable("LDAP_BIND_PASSWORD", "service-account-password", "Machine")
```

> **Note:** System env vars require a new terminal session to be visible. Set them in the current session too for immediate testing:
> ```powershell
> $env:LOCKE_SCIM_TOKEN = "locke_scim_yourorg_xxxxxxxxxxxxxxxx"
> $env:LDAP_BIND_PASSWORD = "service-account-password"
> ```

#### 4. Validate

```powershell
& "C:\Program Files\Locke\locke-connector.exe" sync --config "C:\ProgramData\Locke\locke-connector.yaml" --dry-run
```

Expected output:
```
INF starting incremental sync
INF LDAP search found users count=50
INF creating user user=jsmith
INF creating user user=mwilson
... (one line per user)
INF sync complete created=50 updated=0 disabled=0 deleted=0 skipped=0 errors=0
```

#### 5. Install + Start Service

```powershell
$exe = "C:\Program Files\Locke\locke-connector.exe"
& $exe service install --config "C:\ProgramData\Locke\locke-connector.yaml"
& $exe service start
```

Verify:
```powershell
Get-Service LockeDirectoryConnector | Select-Object Status, StartType
# Should show: Running, Automatic
```

#### 6. Verify Auth Relay

The service maintains a persistent SSE connection to the Locke API. Confirm it's connected:
```powershell
# Check service logs
Get-WinEvent -LogName Application -MaxEvents 20 |
  Where-Object { $_.Message -like "*locke*" } |
  Format-List TimeCreated, Message
```

Look for: `auth relay stream connected`

Then test login: have a synced user sign in at Locke ID with their AD username + AD password.

### Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `LDAP Result Code 200 "Network Error": tls: failed to verify certificate` | DC cert expired or issued by internal CA not trusted by connector host | Add `-SkipTLSVerify` for testing, or install the CA root cert on the connector host |
| `LDAP Result Code 49 "Invalid Credentials": data 52e` | Wrong service account DN or password | Run `Get-ADUser svc-locke-connector \| Select DistinguishedName` and verify `bind_dn` matches exactly |
| `ad_users: 0` (no users found) | Group DN wrong in user_filter, or LockeUsers is an OU not a Security Group | Verify with `Get-ADGroup LockeUsers \| Select DistinguishedName` — must be a Security Group, not an OU |
| `syncing new group` spam (all AD groups) | No `group_include` filter | Add `group_include: ["LockeUsers"]` under `sync:` in config |
| 503 "directory connector offline" on login | SSE relay not connected | Check service is running; check config path is absolute in service registration (`sc.exe qc LockeDirectoryConnector`); verify SCIM token is correct |
| Config not found when service starts | Relative config path — service runs from `C:\Windows\system32` | Reinstall with absolute path: `service install --config "C:\ProgramData\Locke\locke-connector.yaml"` |
| Env vars not picked up by service | Set after service was installed; Local System didn't inherit them | Restart the service, or bake vars into registry: `Set-ItemProperty "HKLM:\SYSTEM\CurrentControlSet\Services\LockeDirectoryConnector" -Name Environment -Value @("LOCKE_SCIM_TOKEN=...", "LDAP_BIND_PASSWORD=...")` |

### Configuration Reference

See `configs/example.yaml` for the full annotated config. Key settings:

| Setting | Default | Description |
|---------|---------|-------------|
| `ldap.host` | — | DC hostname (FQDN recommended) |
| `ldap.port` | 636 | 636 for LDAPS, 389 for StartTLS |
| `ldap.tls_skip_verify` | false | Skip TLS cert verification (testing only) |
| `sync.interval` | 5m | How often to check for changes |
| `sync.full_sync_interval` | 6h | How often to do full reconciliation |
| `sync.user_filter` | all users | LDAP filter — use `memberOf` to scope to a group |
| `sync.group_include` | all groups | Whitelist of group names to sync |
| `relay.enabled` | true | Enable LDAP auth relay (SSE stream) |
| `mapping.user_id_format` | base64 | Must match ADFS SAML assertion format |

### Deployment on Linux

```bash
sudo cp locke-connector-linux-amd64 /usr/local/bin/locke-connector
sudo chmod +x /usr/local/bin/locke-connector
sudo mkdir -p /etc/locke-connector
sudo cp locke-connector.yaml /etc/locke-connector/

export LDAP_BIND_PASSWORD="..."
export LOCKE_SCIM_TOKEN="..."

# Test
locke-connector sync --config /etc/locke-connector/locke-connector.yaml --dry-run

# Run as daemon
locke-connector run --config /etc/locke-connector/locke-connector.yaml
```

## Quick Start (Development)

```bash
# Build for your platform
make local

# Copy and fill in config
cp configs/example.yaml ./locke-connector.yaml

# Set secrets via env vars
export LDAP_BIND_PASSWORD="your-service-account-password"
export LOCKE_SCIM_TOKEN="locke_scim_yourorg_xxxxx"

# Test connection (shows what would happen, no changes)
./dist/locke-connector sync --dry-run

# Run one-shot full sync
./dist/locke-connector sync --full

# Run as daemon (syncs every 5 minutes + auth relay)
./dist/locke-connector run
```

## AD Service Account Setup

The connector needs a **read-only** service account with minimal permissions:

- Create a dedicated service account (e.g., `svc-locke-connector`)
- Delegate **read-only** access to user/group OUs only (not domain-wide)
- Grant read access to `CN=Deleted Objects` for real-time delete detection
- Deny interactive logon (defense in depth)
- The account cannot write to AD, read passwords, or access anything outside delegated OUs

## Security Model

- **LDAP:** Read-only service account, TLS mandatory (LDAPS or StartTLS)
- **SCIM token:** Only secret stored — encrypted at rest or supplied via env var
- **Auth relay:** Outbound-only SSE — no inbound firewall rules needed
- **No passwords stored:** AD passwords are used for a one-time LDAP bind and immediately discarded
- **objectGUID as join key:** Immutable identifier — survives username renames

## Releases

Download binaries from [GitHub Releases](https://github.com/locke-inc/directory-connector/releases).

| Platform | Binary |
|----------|--------|
| Windows x64 | `locke-connector-windows-amd64.exe` |
| Linux x64 | `locke-connector-linux-amd64` |
| macOS ARM64 | `locke-connector-darwin-arm64` |

## Current Status

**v0.1.0** — First production deployment. Includes:
- SCIM user/group provisioning from Active Directory
- LDAP bind authentication relay (SSE-based)
- Windows Service support (auto-start, auto-recovery)
- Incremental + full sync with configurable intervals
- Nested group membership support
- Automated PowerShell installer
