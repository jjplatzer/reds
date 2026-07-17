[CmdletBinding()]
param(
    [string]$InputFile = "windows\winres.json",

    [string]$OutputFile = "build\winres.json"
)

$ErrorActionPreference = "Stop"

$Root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $Root

$Version = $env:REDS_VERSION
if ([string]::IsNullOrWhiteSpace($Version)) {
    if (Test-Path "VERSION") {
        $Version = (Get-Content "VERSION" -Raw).Trim()
    } else {
        $Version = "dev"
    }
}
if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = "dev"
}

$VersionCore = $env:REDS_VERSION_CORE
if ([string]::IsNullOrWhiteSpace($VersionCore)) {
    $VersionCore = ($Version -split "-", 2)[0]
}
if ($VersionCore -notmatch "^[0-9]+\.[0-9]+\.[0-9]+$") {
    $VersionCore = "0.0.0"
}

$Parts = $VersionCore.Split(".")
$FixedVersion = "$($Parts[0]).$($Parts[1]).$($Parts[2]).0"

$Json = Get-Content $InputFile -Raw | ConvertFrom-Json

$VersionBlock = $Json.RT_VERSION.'#1'.'0000'
$VersionBlock.fixed.file_version = $FixedVersion
$VersionBlock.fixed.product_version = $FixedVersion

$Info = $VersionBlock.info.'0409'
$Info.FileVersion = $Version
$Info.ProductVersion = $Version

$OutputParent = Split-Path $OutputFile
if (-not [string]::IsNullOrWhiteSpace($OutputParent)) {
    New-Item -ItemType Directory -Force $OutputParent | Out-Null
}

$Json |
    ConvertTo-Json -Depth 50 |
    Set-Content -Encoding UTF8 $OutputFile

Write-Host "[resources] Wrote $OutputFile with REDS version $Version"
