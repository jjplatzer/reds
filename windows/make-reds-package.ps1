[CmdletBinding()]
param(
    [string]$Binary = "build\REDS.exe",

    [string]$OutputDirectory = "build\REDS-Windows",

    [string]$Archive = "build\REDS-Windows.zip"
)

$ErrorActionPreference = "Stop"

$Root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $Root

$BinaryPath = (Resolve-Path $Binary).Path
$RepositoryRoot = (Get-Location).Path
$OutputPath = Join-Path $RepositoryRoot $OutputDirectory
$ArchivePath = Join-Path $RepositoryRoot $Archive
$ExecutablePath = Join-Path $OutputPath "REDS.exe"

if (-not $env:MSYS2_UCRT64_BIN) {
    throw @"
MSYS2_UCRT64_BIN is not set.

Run this script through build.bat --package,
which configures the UCRT64 toolchain first.
"@
}

$Objdump = Join-Path $env:MSYS2_UCRT64_BIN "objdump.exe"
if (-not (Test-Path $Objdump)) {
    throw "objdump.exe was not found at $Objdump"
}

Write-Host "[package] Creating portable Windows application..."

if (Test-Path $OutputPath) {
    Remove-Item -Recurse -Force $OutputPath
}

New-Item -ItemType Directory -Force $OutputPath | Out-Null

Copy-Item $BinaryPath $ExecutablePath

Write-Host "[package] Copying REDS resources..."

Copy-Item -Recurse -Force "resources" (Join-Path $OutputPath "resources")
Copy-Item -Recurse -Force "fonts" (Join-Path $OutputPath "fonts")

New-Item -ItemType Directory -Force (Join-Path $OutputPath "asdex") | Out-Null
Copy-Item -Recurse -Force "asdex\surface" (Join-Path $OutputPath "asdex\surface")

function Get-ImportedDlls {
    param(
        [Parameter(Mandatory)]
        [string]$Path
    )

    $Output = & $Objdump -p $Path
    if ($LASTEXITCODE -ne 0) {
        throw "objdump failed for $Path"
    }

    foreach ($Line in $Output) {
        if ($Line -match "DLL Name:\s*(.+)$") {
            $Matches[1].Trim()
        }
    }
}

Write-Host "[package] Resolving native dependencies..."

$Queue = New-Object "System.Collections.Generic.Queue[string]"
$Queue.Enqueue($ExecutablePath)
$Processed = @{}

while ($Queue.Count -gt 0) {
    $Current = $Queue.Dequeue()
    $ResolvedCurrent = (Resolve-Path $Current).Path
    $CurrentKey = $ResolvedCurrent.ToLowerInvariant()

    if ($Processed.ContainsKey($CurrentKey)) {
        continue
    }

    $Processed[$CurrentKey] = $true

    foreach ($Dll in Get-ImportedDlls $ResolvedCurrent) {
        $LocalDll = Join-Path $OutputPath $Dll

        if (Test-Path $LocalDll) {
            $Queue.Enqueue($LocalDll)
            continue
        }

        $ToolchainDll = Join-Path $env:MSYS2_UCRT64_BIN $Dll

        if (Test-Path $ToolchainDll) {
            Write-Host "[package] Bundling $Dll"
            Copy-Item -Force $ToolchainDll $LocalDll
            $Queue.Enqueue($LocalDll)
            continue
        }

        # Not present in MSYS2 UCRT64 means it is expected to be provided by
        # Windows itself.
        Write-Host "[package] System dependency: $Dll"
    }
}

Write-Host "[package] Validating application..."

$RequiredPaths = @(
    $ExecutablePath,
    (Join-Path $OutputPath "resources\videomaps\asdex"),
    (Join-Path $OutputPath "resources\configs\asdex"),
    (Join-Path $OutputPath "resources\audio\asdex"),
    (Join-Path $OutputPath "fonts"),
    (Join-Path $OutputPath "asdex\surface")
)

foreach ($Path in $RequiredPaths) {
    if (-not (Test-Path $Path)) {
        throw "Missing packaged path: $Path"
    }
}

$VersionInfo = (Get-Item $ExecutablePath).VersionInfo

if ($VersionInfo.ProductName -ne "REDS") {
    throw @"
Windows version resources were not embedded.
ProductName was "$($VersionInfo.ProductName)".
"@
}

$PeInformation = & $Objdump -p $ExecutablePath
if (-not ($PeInformation -match "Windows GUI")) {
    throw "REDS.exe is not a Windows GUI executable."
}

Write-Host ""
Write-Host "Embedded version information:"
Write-Host "  Product: $($VersionInfo.ProductName)"
Write-Host "  Version: $($VersionInfo.ProductVersion)"

if (Test-Path $ArchivePath) {
    Remove-Item -Force $ArchivePath
}

Write-Host "[package] Creating $ArchivePath..."

Compress-Archive `
    -Path $OutputPath `
    -DestinationPath $ArchivePath `
    -CompressionLevel Optimal

Write-Host ""
Write-Host "[package] Created $OutputPath"
Write-Host "[package] Created $ArchivePath"
