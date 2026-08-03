#Requires -Version 5.1
<#
.SYNOPSIS
    Lele universal installer for Windows.

.DESCRIPTION
    Detects your architecture, downloads the correct release binary from
    GitHub Releases, verifies its SHA256 checksum, and installs it.

    Usage (one-liner):
      irm https://raw.githubusercontent.com/xilistudios/lele/main/install.ps1 | iex

    Install a specific version:
      & ([scriptblock]::Create((irm https://raw.githubusercontent.com/xilistudios/lele/main/install.ps1))) -Version v0.2.0

    Custom install directory:
      & ([scriptblock]::Create((irm https://raw.githubusercontent.com/xilistudios/lele/main/install.ps1))) -InstallDir C:\tools\lele

    Or run this file directly:
      .\install.ps1 [-Version <tag>] [-InstallDir <path>] [-NoPath]

.PARAMETER Version
    Install a specific version (e.g. v0.2.0). Defaults to the latest release.

.PARAMETER InstallDir
    Installation directory (default: $env:LOCALAPPDATA\lele).
    The binary is placed in <InstallDir>\bin\lele.exe.

.PARAMETER NoPath
    Do not add the install directory to the user PATH.

.PARAMETER Help
    Show usage information.
#>

[CmdletBinding()]
param(
    [Alias("v")][string]$Version = "",
    [Alias("p")][string]$InstallDir = "",
    [switch]$NoPath,
    [Alias("h")][switch]$Help
)

$ErrorActionPreference = "Stop"

# PowerShell 5.1 defaults to TLS 1.0/1.1, which GitHub rejects.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$Repo   = "xilistudios/lele"
$Binary = "lele"

# ── Helpers ──────────────────────────────────────────────────────────

function Write-Log  { param([string]$Msg) Write-Host "  $Msg" }
function Write-Info { param([string]$Msg) Write-Host "==> $Msg" -ForegroundColor Blue }
function Write-Ok   { param([string]$Msg) Write-Host "==> $Msg" -ForegroundColor Green }
function Stop-Install {
    param([string]$Msg)
    Write-Host "Error: $Msg" -ForegroundColor Red
    exit 1
}

function Get-TextFromUrl {
    param([Parameter(Mandatory)][string]$Url)
    try {
        return (Invoke-WebRequest -Uri $Url -UseBasicParsing).Content
    } catch {
        Stop-Install "Failed to fetch ${Url}: $($_.Exception.Message)"
    }
}

function Save-FileFromUrl {
    param(
        [Parameter(Mandatory)][string]$Url,
        [Parameter(Mandatory)][string]$Destination
    )
    try {
        Invoke-WebRequest -Uri $Url -OutFile $Destination -UseBasicParsing
    } catch {
        Stop-Install "Failed to download ${Url}: $($_.Exception.Message)"
    }
}

# ── Help ─────────────────────────────────────────────────────────────

if ($Help) {
    @"
Lele Installer (Windows)

Options:
  -Version, -v      Install a specific version (e.g. v0.2.0)
  -InstallDir, -p   Installation directory (default: `$env:LOCALAPPDATA\lele)
  -NoPath           Do not add the install directory to the user PATH
  -Help, -h         Show this help

The binary is placed in <InstallDir>\bin\lele.exe.
"@ | Write-Host
    exit 0
}

# ── Detect platform ──────────────────────────────────────────────────

function Get-LeleArch {
    # PROCESSOR_ARCHITEW6432 is set when running 32-bit PowerShell on 64-bit Windows.
    $arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    switch ($arch) {
        "AMD64" { return "x86_64" }
        "ARM64" { return "arm64" }
        default { Stop-Install "Unsupported architecture: $arch (only x86_64 and arm64 are supported on Windows)." }
    }
}

# ── Resolve latest version ───────────────────────────────────────────

function Get-LatestVersion {
    $resp = Get-TextFromUrl "https://api.github.com/repos/$Repo/releases/latest"
    if ($resp -match '"tag_name"\s*:\s*"([^"]+)"') {
        return $Matches[1]
    }
    Stop-Install "Could not determine latest release."
}

# ── Main ─────────────────────────────────────────────────────────────

$Arch = Get-LeleArch

if (-not $InstallDir) {
    $InstallDir = Join-Path $env:LOCALAPPDATA "lele"
}
$BinDir = Join-Path $InstallDir "bin"

Write-Info "Detected platform: Windows/$Arch"

if (-not $Version) {
    Write-Info "Fetching latest release..."
    $Version = Get-LatestVersion
}

# Strip leading 'v' for the checksums file name.
$VersionNum = $Version -replace '^v', ''

$Archive = "${Binary}_Windows_${Arch}.zip"
$Url = "https://github.com/$Repo/releases/download/$Version/$Archive"

Write-Info "Downloading $Binary $Version ..."
Write-Log $Url

$TmpDir = Join-Path $env:TEMP "lele-install-$(Get-Random)"
New-Item -ItemType Directory -Path $TmpDir -Force | Out-Null

try {
    $ArchivePath = Join-Path $TmpDir $Archive
    Save-FileFromUrl -Url $Url -Destination $ArchivePath

    # Verify checksum
    $ChecksumsUrl = "https://github.com/$Repo/releases/download/$Version/${Binary}_${VersionNum}_checksums.txt"
    Write-Info "Verifying checksum..."
    $Checksums = Get-TextFromUrl -Url $ChecksumsUrl

    $Expected = $null
    foreach ($line in ($Checksums -split "`r?`n")) {
        if ($line -match "^\s*([0-9a-fA-F]{64})\s+\*?$([regex]::Escape($Archive))\s*$") {
            $Expected = $Matches[1].ToLowerInvariant()
            break
        }
    }
    if (-not $Expected) {
        Stop-Install "Archive '$Archive' not found in checksums file."
    }

    $Actual = (Get-FileHash -Path $ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($Actual -ne $Expected) {
        Stop-Install "Checksum mismatch!`n  Expected: $Expected`n  Got:      $Actual"
    }
    Write-Log "Checksum OK"

    # Extract
    Write-Info "Extracting..."
    $OutDir = Join-Path $TmpDir "out"
    Expand-Archive -Path $ArchivePath -DestinationPath $OutDir -Force

    $Extracted = Join-Path $OutDir "$Binary.exe"
    if (-not (Test-Path $Extracted)) {
        Stop-Install "Binary not found in archive. Contents: $((Get-ChildItem $OutDir).Name -join ', ')"
    }

    # Install
    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
    $Target = Join-Path $BinDir "$Binary.exe"

    # Stop a running lele process so the binary can be replaced.
    Get-Process -Name $Binary -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue

    Copy-Item -Path $Extracted -Destination $Target -Force

    Write-Ok "Installed $Binary $Version to $Target"

    # PATH handling
    $InPath = $false
    foreach ($p in ($env:PATH -split ";" | Where-Object { $_ })) {
        if ($p.TrimEnd('\','/') -ieq $BinDir.TrimEnd('\','/')) { $InPath = $true; break }
    }
    if ($NoPath) {
        Write-Log "Skipping PATH update (-NoPath)."
    } elseif ($InPath) {
        Write-Log "$BinDir is already on your PATH."
    } else {
        Write-Info "Adding $BinDir to your user PATH..."
        $UserPath = [Environment]::GetEnvironmentVariable("PATH", "User")
        $NewPath = if ($UserPath) { "$UserPath;$BinDir" } else { $BinDir }
        [Environment]::SetEnvironmentVariable("PATH", $NewPath, "User")
        # Also update the current session so 'lele' works immediately.
        $env:PATH = "$env:PATH;$BinDir"
        Write-Log "PATH updated. Open a new terminal if 'lele' is not found."
    }

    Write-Log "Run 'lele --help' to get started."
} finally {
    Remove-Item -Path $TmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
