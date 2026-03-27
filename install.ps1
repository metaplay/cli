#!/usr/bin/env pwsh
#
# PREVIEW: This script is mainly intended for internal use and may change without notice.
#
# Installer for the Metaplay CLI 'metaplay' on Windows.
# Works on x86_64 and arm64.
# Usage: .\install.ps1 [-Version X.Y.Z] [-Verbose]
#
# How to use:
#
# Install the latest version (auto-detect platform):
#   irm https://metaplay.github.io/cli/install.ps1 | iex
# Install a specific version:
#   $env:METAPLAY_VERSION='1.2.3'; irm https://metaplay.github.io/cli/install.ps1 | iex
# Install latest dev version:
#   $env:METAPLAY_VERSION='latest-dev'; irm https://metaplay.github.io/cli/install.ps1 | iex
# Or run locally:
#   .\install.ps1 -Version 1.2.3
#
# Note: When piping via iex, use $env:METAPLAY_VERSION for options.
# The -Version, -Verbose, and -Help switches only work when running the script directly.

# Wrap in a function to ensure the entire script is downloaded before execution (like install.sh),
# and to allow using 'return' for early exit without closing the user's terminal when run via iex.
$script:InstallFailed = $false
function Install-MetaplayCLI {
[CmdletBinding()]
param(
    [string]$Version = $env:METAPLAY_VERSION,
    [switch]$Help
)

$ErrorActionPreference = 'Stop'

# Ensure TLS 1.2 is available (Windows PowerShell 5.1 defaults to older protocols that GitHub rejects)
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

# --- Adjustable variables ---
$Repo = 'metaplay/cli'
$BinaryName = 'metaplay.exe'
$InstallDir = Join-Path (Join-Path $env:LOCALAPPDATA 'metaplay') 'bin'
$DownloadBase = "https://github.com/$Repo/releases/download"
# ----------------------------

function Print-Info($msg) { Write-Host "[installer] $msg" }
function Print-Success($msg) { Write-Host "[installer] $msg" -ForegroundColor Green }
function Print-Error($msg) { Write-Host "[installer] ERROR: $msg" -ForegroundColor Red }
function Print-Verbose($msg) {
    if ($VerbosePreference -eq 'Continue') { Write-Host "[verbose] $msg" }
}

if ($Help) {
    Write-Host @"
Usage: .\install.ps1 [OPTIONS]

Options:
  -Version X.Y.Z   Install a specific version (e.g. 1.2.3).
  -Verbose          Enable verbose (debug) output.
  -Help             Show this help message.

By default, installs the latest version for your platform.

Alternative install methods:
  scoop:       scoop bucket add metaplay https://github.com/metaplay/scoop-bucket && scoop install metaplay
  chocolatey:  choco install metaplay
"@
    return
}

# Detect architecture
$Arch = $null
switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { $Arch = 'x86_64' }
    'ARM64' { $Arch = 'arm64' }
    default {
        Print-Error "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE"
        $script:InstallFailed = $true; return
    }
}

Print-Verbose "Arch: $Arch"
Print-Verbose "Install dir: $InstallDir"

# Ensure curl.exe is available (pre-installed on Windows 10+ and all GitHub Actions runners)
if (-not (Get-Command 'curl.exe' -ErrorAction SilentlyContinue)) {
    Print-Error "curl.exe is required but not found. It is included with Windows 10 and later."
    $script:InstallFailed = $true; return
}

# Common curl flags, mirroring install.sh
$CurlRetryFlags = @('--retry', '10', '--retry-all-errors', '--retry-max-time', '60')

$ZipName = "MetaplayCLI_Windows_$Arch.zip"

# Resolve version and construct download URL
if (-not $Version -or $Version -eq 'latest') {
    # Use /releases/latest/download/ -- GitHub resolves the version via redirect.
    # This avoids Invoke-WebRequest which can hang indefinitely on some Windows environments.
    $DownloadUrl = "https://github.com/$Repo/releases/latest/download/$ZipName"
    Print-Info "Installing latest '$BinaryName' for Windows/$Arch to $InstallDir..."
} elseif ($Version -eq 'latest-dev') {
    Print-Verbose "Version specified as 'latest-dev'. Finding latest development release..."
    # Fetch all releases (newest first) and parse the tag_name of the first one.
    # Note: Uses a rate-limited URL but since this is for internal use only, it's fine.
    $releaseJson = curl.exe -sSf @CurlRetryFlags "https://api.github.com/repos/$Repo/releases"
    if ($LASTEXITCODE -ne 0) {
        Print-Error "Failed to fetch releases to determine 'latest-dev' version."
        $script:InstallFailed = $true; return
    }
    $Version = ($releaseJson -join "`n" | ConvertFrom-Json)[0].tag_name
    Print-Verbose "Detected latest development version: $Version"
    $DownloadUrl = "$DownloadBase/$Version/$ZipName"
    Print-Info "Installing '$BinaryName' v$Version for Windows/$Arch to $InstallDir..."
} else {
    $DownloadUrl = "$DownloadBase/$Version/$ZipName"
    Print-Info "Installing '$BinaryName' v$Version for Windows/$Arch to $InstallDir..."
}

Print-Verbose "Download URL: $DownloadUrl"

# Download, extract, and install (with guaranteed temp dir cleanup)
$TmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "metaplay-install-$([System.Guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Path $TmpDir -Force | Out-Null
$ZipPath = Join-Path $TmpDir $ZipName
$DestBinary = Join-Path $InstallDir "$BinaryName"

try {
    # If version was not specified, resolve it from the first redirect of /releases/latest/download/.
    # GitHub redirects: /releases/latest/download/X.zip -> /releases/download/<version>/X.zip -> CDN
    # A HEAD request to get just the first redirect is fast and mirrors what install.sh does with curl -sI.
    if (-not $Version -or $Version -eq 'latest') {
        $redirectUrl = curl.exe -fsI @CurlRetryFlags -o NUL -w '%{redirect_url}' $DownloadUrl
        if ($LASTEXITCODE -ne 0) {
            Print-Error "Failed to resolve latest version from $DownloadUrl"
            $script:InstallFailed = $true; return
        }
        $Version = ($redirectUrl -split '/')[-2]
        # Use the resolved URL for the actual download
        $DownloadUrl = $redirectUrl
        Print-Info "Resolved latest version: v$Version"
    }

    # Download using curl.exe (avoids Invoke-WebRequest which can hang on some Windows environments).
    # -L follows redirects (from GitHub to CDN).
    curl.exe -fsSL @CurlRetryFlags -o $ZipPath $DownloadUrl
    if ($LASTEXITCODE -ne 0) {
        Print-Error "Failed to download from $DownloadUrl"
        $script:InstallFailed = $true; return
    }

    # Extract the binary
    try {
        Expand-Archive -Path $ZipPath -DestinationPath $TmpDir -Force
    } catch {
        Print-Error 'Failed to extract archive.'
        Print-Error $_.Exception.Message
        $script:InstallFailed = $true; return
    }

    $ExtractedBinary = Join-Path $TmpDir "$BinaryName"
    if (-not (Test-Path $ExtractedBinary)) {
        Print-Error "Downloaded archive does not contain the expected binary '$BinaryName'."
        $script:InstallFailed = $true; return
    }

    # Ensure install directory exists and move binary
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    try {
        Move-Item -Path $ExtractedBinary -Destination $DestBinary -Force
    } catch {
        Print-Error "Failed to install binary to $DestBinary"
        if (Test-Path $DestBinary) {
            Print-Error "The file may be in use. Close any running '$BinaryName' processes and try again."
        } else {
            Print-Error $_.Exception.Message
        }
        $script:InstallFailed = $true; return
    }
} finally {
    # Clean up temp dir (mirrors install.sh's trap-based cleanup)
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}

# Add to user PATH if not already present (case-insensitive comparison for Windows paths)
$UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$PathEntries = ($UserPath -split ';') | Where-Object { $_ -ne '' }
if ($PathEntries | Where-Object { $_ -ieq $InstallDir }) {
    Print-Verbose "$InstallDir is already in your PATH."
} else {
    Print-Info "Adding $InstallDir to your user PATH..."
    $NewPath = (@($InstallDir) + $PathEntries) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $NewPath, 'User')
    # Also update current session
    $env:Path = "$InstallDir;$env:Path"
}

# Verify installation
if (Get-Command "$BinaryName" -ErrorAction SilentlyContinue) {
    # Temporarily lower ErrorActionPreference: PS5 treats any stderr output from native
    # commands as a terminating error under 'Stop', even with 2>$null.
    $prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
    $InstalledVersion = & $DestBinary version 2>$null
    $ErrorActionPreference = $prevEAP
    Print-Success "'$BinaryName' v$InstalledVersion successfully installed!"
} else {
    Print-Success "'$BinaryName' v$Version installed to $InstallDir."
    Print-Info 'Restart your terminal for the PATH update to take effect.'
}

}

Install-MetaplayCLI @args

# When running as a script file (not via iex), exit with error code on failure.
# $PSCommandPath is empty when evaluated via iex — we must not call exit in that
# case because it would close the user's terminal.
if ($script:InstallFailed -and $PSCommandPath) {
    exit 1
}
