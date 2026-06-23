#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Installs the Locke Directory Connector on a Windows server.

.DESCRIPTION
    Automates the full installation process:
    - Creates install directories
    - Creates AD service account (if on a domain controller)
    - Creates AD security group for synced users
    - Copies binary and config
    - Sets environment variables
    - Installs and starts the Windows Service

.PARAMETER ScimToken
    The SCIM token from Locke Armory (required).

.PARAMETER LdapPassword
    Password for the LDAP service account (required).

.PARAMETER ConfigFile
    Path to the YAML config file to install. If not provided, prompts for LDAP details and generates one.

.PARAMETER DomainController
    FQDN of the domain controller (e.g., dc01.corp.example.com). Required if no ConfigFile.

.PARAMETER BaseDN
    LDAP base DN (e.g., DC=corp,DC=example,DC=com). Required if no ConfigFile.

.PARAMETER ServiceAccountName
    Name for the AD service account. Default: svc-locke-connector

.PARAMETER ServiceAccountOU
    OU path to create the service account in (e.g., "OU=Service Accounts,DC=corp,DC=example,DC=com").
    If not specified, uses the default Users container.

.PARAMETER GroupName
    Name for the AD security group. Default: LockeUsers

.PARAMETER GroupOU
    OU path to create the group in. If not specified, uses the default Users container.

.PARAMETER ApiUrl
    Locke API URL. Default: https://api.locke.id

.PARAMETER InstallDir
    Binary install location. Default: C:\Program Files\Locke

.PARAMETER ConfigDir
    Config file location. Default: C:\ProgramData\Locke

.PARAMETER BinaryPath
    Path to the connector .exe. Default: locke-connector-windows-amd64.exe in current directory.

.PARAMETER SkipADSetup
    Skip AD service account and group creation (use if they already exist).

.PARAMETER SkipTLSVerify
    Add tls_skip_verify: true to generated config (for self-signed DC certs).

.EXAMPLE
    # Full install with AD setup
    .\Install-LockeConnector.ps1 -ScimToken "locke_scim_yourorg_xxx" -LdapPassword "P@ssw0rd" `
        -DomainController "dc01.corp.example.com" -BaseDN "DC=corp,DC=example,DC=com"

.EXAMPLE
    # Install with existing config file, skip AD setup
    .\Install-LockeConnector.ps1 -ScimToken "locke_scim_yourorg_xxx" -LdapPassword "P@ssw0rd" `
        -ConfigFile "C:\ProgramData\Locke\locke-connector.yaml" -SkipADSetup
#>

param(
    [Parameter(Mandatory=$true)]
    [string]$ScimToken,

    [Parameter(Mandatory=$true)]
    [string]$LdapPassword,

    [string]$ConfigFile,

    [string]$DomainController,

    [string]$BaseDN,

    [string]$ServiceAccountName = "svc-locke-connector",

    [string]$ServiceAccountOU,

    [string]$GroupName = "LockeUsers",

    [string]$GroupOU,

    [string]$ApiUrl = "https://api.locke.id",

    [string]$InstallDir = "C:\Program Files\Locke",

    [string]$ConfigDir = "C:\ProgramData\Locke",

    [string]$BinaryPath,

    [switch]$SkipADSetup,

    [switch]$SkipTLSVerify
)

$ErrorActionPreference = "Stop"

function Write-Step($msg) { Write-Host "`n[$((Get-Date).ToString('HH:mm:ss'))] $msg" -ForegroundColor Cyan }
function Write-OK($msg) { Write-Host "  OK: $msg" -ForegroundColor Green }
function Write-Skip($msg) { Write-Host "  SKIP: $msg" -ForegroundColor Yellow }

# --- Validate inputs ---
if (-not $ConfigFile -and (-not $DomainController -or -not $BaseDN)) {
    throw "Either -ConfigFile or both -DomainController and -BaseDN are required."
}

# --- Find binary ---
if (-not $BinaryPath) {
    $BinaryPath = Join-Path (Get-Location) "locke-connector-windows-amd64.exe"
}
if (-not (Test-Path $BinaryPath)) {
    throw "Binary not found at: $BinaryPath"
}
Write-Step "Binary found: $BinaryPath"

# --- AD Setup ---
if (-not $SkipADSetup) {
    Write-Step "Setting up Active Directory objects..."

    # Check if AD module is available
    if (-not (Get-Module -ListAvailable -Name ActiveDirectory)) {
        throw "ActiveDirectory PowerShell module not found. Install RSAT or run with -SkipADSetup if AD objects already exist."
    }
    Import-Module ActiveDirectory

    # Create service account
    $existingAccount = Get-ADUser -Filter "SamAccountName -eq '$ServiceAccountName'" -ErrorAction SilentlyContinue
    if ($existingAccount) {
        Write-Skip "Service account '$ServiceAccountName' already exists"
        $svcAccountDN = $existingAccount.DistinguishedName
    } else {
        $svcParams = @{
            Name = $ServiceAccountName
            SamAccountName = $ServiceAccountName
            UserPrincipalName = "$ServiceAccountName@$((Get-ADDomain).DNSRoot)"
            AccountPassword = (ConvertTo-SecureString $LdapPassword -AsPlainText -Force)
            Enabled = $true
            PasswordNeverExpires = $true
            CannotChangePassword = $true
            Description = "Locke Directory Connector service account (read-only AD sync)"
        }
        if ($ServiceAccountOU) {
            $svcParams.Path = $ServiceAccountOU
        }
        New-ADUser @svcParams
        $existingAccount = Get-ADUser $ServiceAccountName
        $svcAccountDN = $existingAccount.DistinguishedName
        Write-OK "Created service account: $svcAccountDN"
    }

    # Grant Read Deleted Objects permission
    try {
        $deletedObjectsDN = "CN=Deleted Objects," + (Get-ADDomain).DistinguishedName
        $acl = Get-Acl "AD:\$deletedObjectsDN"
        $sid = $existingAccount.SID
        $rule = New-Object System.DirectoryServices.ActiveDirectoryAccessRule(
            $sid, "GenericRead", "Allow"
        )
        $acl.AddAccessRule($rule)
        Set-Acl "AD:\$deletedObjectsDN" $acl
        Write-OK "Granted Read Deleted Objects permission"
    } catch {
        Write-Host "  WARN: Could not set Read Deleted Objects permission (may require Enterprise Admin): $_" -ForegroundColor Yellow
    }

    # Create security group
    $existingGroup = Get-ADGroup -Filter "Name -eq '$GroupName'" -ErrorAction SilentlyContinue
    if ($existingGroup) {
        Write-Skip "Security group '$GroupName' already exists"
        $groupDN = $existingGroup.DistinguishedName
    } else {
        $grpParams = @{
            Name = $GroupName
            GroupScope = "Global"
            GroupCategory = "Security"
            Description = "Users synced to Locke Identity"
        }
        if ($GroupOU) {
            $grpParams.Path = $GroupOU
        }
        New-ADGroup @grpParams
        $existingGroup = Get-ADGroup $GroupName
        $groupDN = $existingGroup.DistinguishedName
        Write-OK "Created security group: $groupDN"
    }

    $memberCount = (Get-ADGroupMember $GroupName -ErrorAction SilentlyContinue | Measure-Object).Count
    Write-Host "  INFO: '$GroupName' has $memberCount members" -ForegroundColor White
    if ($memberCount -eq 0) {
        Write-Host "  WARN: Add users to '$GroupName' before running sync!" -ForegroundColor Yellow
    }
} else {
    Write-Step "Skipping AD setup (using existing objects)"
    # Discover DNs for config generation
    if (-not $ConfigFile) {
        Import-Module ActiveDirectory -ErrorAction SilentlyContinue
        $existingAccount = Get-ADUser -Filter "SamAccountName -eq '$ServiceAccountName'"
        $existingGroup = Get-ADGroup -Filter "Name -eq '$GroupName'"
        if (-not $existingAccount) { throw "Service account '$ServiceAccountName' not found in AD" }
        if (-not $existingGroup) { throw "Group '$GroupName' not found in AD" }
        $svcAccountDN = $existingAccount.DistinguishedName
        $groupDN = $existingGroup.DistinguishedName
    }
}

# --- Create directories ---
Write-Step "Creating directories..."
foreach ($dir in @($InstallDir, $ConfigDir)) {
    if (-not (Test-Path $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        Write-OK "Created $dir"
    } else {
        Write-Skip "$dir already exists"
    }
}

# --- Copy binary ---
Write-Step "Installing binary..."
$targetBinary = Join-Path $InstallDir "locke-connector.exe"
Copy-Item $BinaryPath $targetBinary -Force
Write-OK "Copied to $targetBinary"

# --- Generate or copy config ---
$targetConfig = Join-Path $ConfigDir "locke-connector.yaml"

if ($ConfigFile) {
    Write-Step "Copying config file..."
    Copy-Item $ConfigFile $targetConfig -Force
    Write-OK "Copied to $targetConfig"
} else {
    Write-Step "Generating config file..."

    $tlsLine = ""
    if ($SkipTLSVerify) {
        $tlsLine = "  tls_skip_verify: true"
    }

    $configContent = @"
locke:
  api_url: "$ApiUrl"
  # Set via environment variable: LOCKE_SCIM_TOKEN

ldap:
  host: "$DomainController"
  port: 636
  tls: true
$tlsLine
  bind_dn: "$svcAccountDN"
  # Set via environment variable: LDAP_BIND_PASSWORD
  base_dn: "$BaseDN"

sync:
  interval: "5m"
  full_sync_interval: "6h"
  user_filter: "(&(objectClass=user)(objectCategory=person)(memberOf:1.2.840.113556.1.4.1941:=$groupDN))"
  group_include: ["$GroupName"]

relay:
  enabled: true

mapping:
  username: "sAMAccountName"
  email: "mail"
  user_id: "objectGUID"
  user_id_format: "base64"

state:
  path: "$($ConfigDir -replace '\\','/')/locke-connector.db"

logging:
  file: "$($ConfigDir -replace '\\','/')/locke-connector.log"
  level: "info"
"@

    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($targetConfig, $configContent, $utf8NoBom)
    Write-OK "Generated $targetConfig"
}

# --- Set environment variables ---
Write-Step "Setting environment variables..."
[System.Environment]::SetEnvironmentVariable("LOCKE_SCIM_TOKEN", $ScimToken, "Machine")
[System.Environment]::SetEnvironmentVariable("LDAP_BIND_PASSWORD", $LdapPassword, "Machine")
Write-OK "Set LOCKE_SCIM_TOKEN and LDAP_BIND_PASSWORD (Machine scope)"

# --- Dry run ---
Write-Step "Running dry-run sync to validate configuration..."
$env:LOCKE_SCIM_TOKEN = $ScimToken
$env:LDAP_BIND_PASSWORD = $LdapPassword

$dryRunOutput = & $targetBinary sync --config $targetConfig --dry-run 2>&1
$dryRunExit = $LASTEXITCODE

Write-Host ($dryRunOutput | Out-String)

if ($dryRunExit -ne 0) {
    throw "Dry-run failed (exit code $dryRunExit). Fix the config at $targetConfig and re-run."
}
Write-OK "Dry-run passed"

# --- Install service ---
Write-Step "Installing Windows Service..."

# Remove existing service if present
$existingService = Get-Service -Name "LockeDirectoryConnector" -ErrorAction SilentlyContinue
if ($existingService) {
    Write-Host "  Removing existing service..." -ForegroundColor Yellow
    & $targetBinary service stop 2>$null
    & $targetBinary service uninstall 2>$null
    Start-Sleep -Seconds 5
}

& $targetBinary service install --config $targetConfig
if ($LASTEXITCODE -ne 0) {
    throw "Service install failed"
}
Write-OK "Service installed"

# Ensure the service binary path includes --config (the Go binary may not persist it)
$regKey = "HKLM:\SYSTEM\CurrentControlSet\Services\LockeDirectoryConnector"
if (Test-Path $regKey) {
    $imagePath = (Get-ItemProperty $regKey -Name ImagePath).ImagePath
    if ($imagePath -notlike "*--config*") {
        $correctPath = "`"$targetBinary`" run --config `"$targetConfig`""
        Set-ItemProperty $regKey -Name ImagePath -Value $correctPath
        Write-OK "Fixed service binary path to include --config"
    }

    Set-ItemProperty $regKey -Name Environment -Type MultiString -Value @(
        "LOCKE_SCIM_TOKEN=$ScimToken",
        "LDAP_BIND_PASSWORD=$LdapPassword"
    )
    Write-OK "Environment variables baked into service registry"
}

# --- Start service ---
Write-Step "Starting service..."
& $targetBinary service start
if ($LASTEXITCODE -ne 0) {
    throw "Service start failed"
}
Start-Sleep -Seconds 3

$svc = Get-Service "LockeDirectoryConnector"
if ($svc.Status -eq "Running") {
    Write-OK "Service is running (StartType: $($svc.StartType))"
} else {
    Write-Host "  WARN: Service status is $($svc.Status)" -ForegroundColor Yellow
}

# --- Summary ---
Write-Host "`n" -NoNewline
Write-Host "=====================================" -ForegroundColor Green
Write-Host " INSTALLATION COMPLETE" -ForegroundColor Green
Write-Host "=====================================" -ForegroundColor Green
Write-Host ""
Write-Host "  Binary:    $targetBinary"
Write-Host "  Config:    $targetConfig"
Write-Host "  State DB:  $ConfigDir\locke-connector.db"
Write-Host "  Logs:      $ConfigDir\locke-connector.log"
Write-Host "  Service:   LockeDirectoryConnector (Auto Start)"
Write-Host ""
Write-Host "  Next steps:" -ForegroundColor White
Write-Host "    1. Add users to the '$GroupName' AD group (if not done)"
Write-Host "    2. Test login: have a synced user sign in at Locke ID"
Write-Host "    3. Deploy Chrome extension: .\Install-LockeExtensionGPO.ps1"
Write-Host "    4. Monitor: Get-WinEvent -LogName Application | Where { `$_.Message -like '*locke*' }"
Write-Host ""
