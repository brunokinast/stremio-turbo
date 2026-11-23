<#
  Reports whether every original project's latest code is merged, and whether
  the web UI uses the latest Stremio core. Exits 1 when something is behind
  (the release gate), 0 when all is current.

  Usage: pwsh scripts/check-upstreams.ps1 [-Markdown]
    -Markdown  print the report as a Markdown list (for the upstream-watch issue)
#>
param([switch]$Markdown)

$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'Upstreams.psm1') -Force

$behind = @()
foreach ($u in $Upstreams) {
    $head = Get-UpstreamHead $u
    $n = Get-UpstreamBehind $u $head
    $line = "$($u.Prefix): $(if ($n) { "$n new upstream commit(s) on $($u.Branch)" } else { 'up to date' })"
    if ($n) { $behind += $line }
    if ($Markdown) { Write-Output "- $line" } else { Write-Host $line }
}
$core = Get-CoreWebVersions
$coreLine = "web: @stremio/stremio-core-web $($core.Used)$(if ($core.Used -ne $core.Latest) { ", latest is $($core.Latest)" } else { ' (latest)' })"
if ($core.Used -ne $core.Latest) { $behind += $coreLine }
if ($Markdown) { Write-Output "- $coreLine" } else { Write-Host $coreLine }

if ($behind.Count) {
    if (-not $Markdown) { Write-Host "`nBehind upstream: run mise run sync." -ForegroundColor Yellow }
    exit 1
}
