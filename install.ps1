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
$BinaryName = 'metaplay'
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

# Helper to invoke web requests with retry logic
function Invoke-WithRetry {
    param(
        [scriptblock]$Action,
        [int]$MaxRetries = 10,
        [int]$DelaySeconds = 2
    )
    for ($i = 0; $i -lt $MaxRetries; $i++) {
        try {
            return (& $Action)
        } catch {
            if ($i -eq $MaxRetries - 1) { throw }
            Print-Verbose "Request failed, retrying in ${DelaySeconds}s... (attempt $($i+1)/$MaxRetries)"
            Start-Sleep -Seconds $DelaySeconds
            $DelaySeconds *= 2
        }
    }
}

# If no version specified, discover latest via GitHub redirect
if (-not $Version) {
    Print-Verbose 'No version specified. Finding latest official release...'
    $latestUrl = "https://github.com/$Repo/releases/latest"

    try {
        $redirectUrl = Invoke-WithRetry {
            try {
                $resp = Invoke-WebRequest -Uri $latestUrl -MaximumRedirection 0 -ErrorAction SilentlyContinue -UseBasicParsing
                # PS7: no exception on redirect, check status
                if ($resp.StatusCode -ge 300 -and $resp.StatusCode -lt 400) {
                    $loc = $resp.Headers['Location']
                    if ($loc -is [array]) { $loc = $loc[0] }
                    return $loc
                }
                return $null
            } catch {
                # PS5 may throw on redirect; extract Location from the exception response
                $exResp = $_.Exception.Response
                if ($exResp) {
                    $loc = $exResp.Headers['Location']
                    if ($loc) { return $loc }
                }
                throw
            }
        }

        if ($redirectUrl) {
            $Version = ($redirectUrl -split '/')[-1]
        }
    } catch {
        Print-Verbose "Redirect method failed: $($_.Exception.Message)"
    }

    if (-not $Version) {
        # Fallback: use the API
        Print-Verbose 'Redirect method failed, falling back to GitHub API...'
        try {
            $releaseInfo = Invoke-WithRetry {
                Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
            }
            $Version = $releaseInfo.tag_name
        } catch {
            Print-Error "Failed to determine latest CLI version. Check your network connection."
            Print-Error $_.Exception.Message
            $script:InstallFailed = $true; return
        }
    }

    Print-Verbose "Detected latest official version: $Version"
} elseif ($Version -eq 'latest-dev') {
    Print-Verbose "Version specified as 'latest-dev'. Finding latest development release..."
    try {
        $releases = Invoke-WithRetry {
            Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases"
        }
        $Version = $releases[0].tag_name
    } catch {
        Print-Error "Failed to fetch releases to determine 'latest-dev' version."
        Print-Error $_.Exception.Message
        $script:InstallFailed = $true; return
    }
    Print-Verbose "Detected latest development version: $Version"
}

if (-not $Version) {
    Print-Error 'Failed to determine CLI version to install. Please check your network connection.'
    Print-Error 'If you are behind a proxy or offline, ensure you can access https://github.com.'
    $script:InstallFailed = $true; return
}

# Construct download URL
$ZipName = "MetaplayCLI_Windows_$Arch.zip"
$DownloadUrl = "$DownloadBase/$Version/$ZipName"

Print-Info "Installing '$BinaryName' v$Version for Windows/$Arch to $InstallDir..."
Print-Verbose "Download URL: $DownloadUrl"

# Download, extract, and install (with guaranteed temp dir cleanup)
$TmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "metaplay-install-$([System.Guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Path $TmpDir -Force | Out-Null
$ZipPath = Join-Path $TmpDir $ZipName
$DestBinary = Join-Path $InstallDir "$BinaryName.exe"

try {
    $prevProgressPref = $ProgressPreference
    try {
        $ProgressPreference = 'SilentlyContinue' # Speeds up Invoke-WebRequest significantly
        Invoke-WithRetry {
            Invoke-WebRequest -Uri $DownloadUrl -OutFile $ZipPath -UseBasicParsing
        }
    } catch {
        Print-Error "Failed to download from $DownloadUrl"
        Print-Error $_.Exception.Message
        $script:InstallFailed = $true; return
    } finally {
        $ProgressPreference = $prevProgressPref
    }

    # Extract the binary
    try {
        Expand-Archive -Path $ZipPath -DestinationPath $TmpDir -Force
    } catch {
        Print-Error 'Failed to extract archive.'
        Print-Error $_.Exception.Message
        $script:InstallFailed = $true; return
    }

    $ExtractedBinary = Join-Path $TmpDir "$BinaryName.exe"
    if (-not (Test-Path $ExtractedBinary)) {
        Print-Error "Downloaded archive does not contain the expected binary '$BinaryName.exe'."
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
if (Get-Command "$BinaryName.exe" -ErrorAction SilentlyContinue) {
    $InstalledVersion = & $DestBinary version 2>$null
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
