#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Creates and links a GPO that force-installs the Locke ID Chrome extension.

.DESCRIPTION
    Automates the full GPO setup on a Domain Controller:
    1. Creates a new Group Policy Object
    2. Sets the Chrome ExtensionInstallForcelist registry policy (User Configuration)
    3. Applies Security Filtering to the target AD group (only group members get the extension)
    4. Links the GPO to the domain (or a specified OU)

    Run this once on any Domain Controller. The extension silently installs on
    Chrome's next launch for all members of the target group.

.PARAMETER Group
    AD security group used for GPO Security Filtering. Only members of this
    group will receive the extension. Default: LockeUsers

.PARAMETER OU
    Distinguished Name of the OU to link the GPO to.
    Default: links to the domain root (applies domain-wide, filtered by group).

.PARAMETER GPOName
    Name of the GPO to create. Default: "Locke Chrome Extension"

.EXAMPLE
    # Full auto — creates GPO filtered to LockeUsers, linked to domain root
    .\Install-LockeExtensionGPO.ps1

.EXAMPLE
    # Custom group and OU
    .\Install-LockeExtensionGPO.ps1 -Group "ITUsers" -OU "OU=Workstations,DC=corp,DC=local"
#>

param(
    [string]$Group = "LockeUsers",
    [string]$OU,
    [string]$GPOName = "Locke Chrome Extension"
)

$ErrorActionPreference = "Stop"

# --- Verify we're on a DC with the GroupPolicy module ---
if (-not (Get-Module -ListAvailable -Name GroupPolicy)) {
    throw "GroupPolicy PowerShell module not found. Run this on a Domain Controller."
}
Import-Module GroupPolicy
Import-Module ActiveDirectory

# --- Verify the target group exists ---
$adGroup = Get-ADGroup -Filter "Name -eq '$Group'" -ErrorAction SilentlyContinue
if (-not $adGroup) {
    throw "AD group '$Group' not found. Create it first or specify a different group with -Group."
}

# --- Check group has members ---
$memberCount = (Get-ADGroupMember -Identity $adGroup -ErrorAction SilentlyContinue | Measure-Object).Count
if ($memberCount -eq 0) {
    Write-Host "WARNING: '$Group' has no members. The extension won't deploy until you add users." -ForegroundColor Yellow
}

# --- Determine link target ---
if (-not $OU) {
    $OU = (Get-ADDomain).DistinguishedName
}

# --- Create GPO (or reuse existing) ---
$gpo = Get-GPO -Name $GPOName -ErrorAction SilentlyContinue
if ($gpo) {
    Write-Host "GPO '$GPOName' already exists — updating." -ForegroundColor Yellow
} else {
    $gpo = New-GPO -Name $GPOName -Comment "Force-installs Locke ID Chrome extension for $Group members"
    Write-Host "Created GPO: $GPOName" -ForegroundColor Green
}

# --- Set Chrome force-install registry value (User Configuration) ---
$ExtensionID = "mgeaadkickfdeiihpliecmjaghpbhcdg"
$UpdateURL = "https://clients2.google.com/service/update2/crx"
$PolicyValue = "$ExtensionID;$UpdateURL"

$regKey = "HKCU\SOFTWARE\Policies\Google\Chrome\ExtensionInstallForcelist"

try {
    $existingValues = Get-GPRegistryValue -Guid $gpo.Id -Key $regKey -ErrorAction Stop
} catch {
    $existingValues = $null
}

$alreadySet = $existingValues | Where-Object { $_.Value -eq $PolicyValue }

if ($alreadySet) {
    Write-Host "Extension already in force-install list." -ForegroundColor Yellow
} else {
    $nextIndex = 1
    if ($existingValues) {
        $maxIndex = $existingValues |
            Where-Object { $_.ValueName -match '^\d+$' } |
            ForEach-Object { [int]($_.ValueName) } |
            Measure-Object -Maximum |
            Select-Object -ExpandProperty Maximum
        if ($maxIndex) { $nextIndex = $maxIndex + 1 }
    }

    Set-GPRegistryValue -Guid $gpo.Id -Key $regKey -ValueName "$nextIndex" -Type String -Value $PolicyValue | Out-Null
    Write-Host "Set ExtensionInstallForcelist entry $nextIndex" -ForegroundColor Green
}

# --- Security Filtering: Authenticated Users gets Read only, target group gets Read + Apply ---
Set-GPPermission -Guid $gpo.Id -PermissionLevel GpoRead -TargetName "Authenticated Users" -TargetType Group -Replace | Out-Null
Set-GPPermission -Guid $gpo.Id -PermissionLevel GpoRead -TargetName $Group -TargetType Group | Out-Null
Set-GPPermission -Guid $gpo.Id -PermissionLevel GpoApply -TargetName $Group -TargetType Group | Out-Null
Write-Host "Security Filtering set to: $Group" -ForegroundColor Green

# --- Link GPO ---
$existingLink = Get-GPInheritance -Target $OU | Select-Object -ExpandProperty GpoLinks |
    Where-Object { $_.DisplayName -eq $GPOName }

if ($existingLink) {
    Write-Host "GPO already linked to $OU" -ForegroundColor Yellow
} else {
    New-GPLink -Guid $gpo.Id -Target $OU -LinkEnabled Yes | Out-Null
    Write-Host "Linked GPO to: $OU" -ForegroundColor Green
}

# --- Done ---
Write-Host ""
Write-Host "=====================================" -ForegroundColor Green
Write-Host " GPO CONFIGURED" -ForegroundColor Green
Write-Host "=====================================" -ForegroundColor Green
Write-Host ""
Write-Host "  GPO Name:     $GPOName"
Write-Host "  Extension:    $ExtensionID"
Write-Host "  Filtered to:  $Group ($($adGroup.DistinguishedName))"
Write-Host "  Linked to:    $OU"
if ($memberCount -gt 0) {
    Write-Host "  Group members: $memberCount users"
}
Write-Host ""
Write-Host "  Chrome will install the extension on next policy refresh (up to 120 min)."
Write-Host "  To push immediately: gpupdate /force (on client machines)" -ForegroundColor Cyan
Write-Host ""
