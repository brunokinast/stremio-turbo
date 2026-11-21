<#
  Merges every original project's latest code into its folder, moves the web
  UI to the latest Stremio core, then runs all tests.

  A merge conflict stops the script with the conflict left in the working
  tree: resolve it, commit, and run the script again.

  Usage: pwsh scripts/sync-upstreams.ps1 [-SkipTests]
#>
param([switch]$SkipTests)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
Import-Module (Join-Path $PSScriptRoot 'Upstreams.psm1') -Force
$root = Split-Path -Parent $PSScriptRoot

if (git -C $root status --porcelain --untracked-files=no) { throw 'commit or stash your changes first' }

foreach ($u in $Upstreams) {
    $head = Get-UpstreamHead $u
    $n = Get-UpstreamBehind $u $head
    if (-not $n) { Write-Host "$($u.Prefix): up to date"; continue }
    Write-Host "$($u.Prefix): merging $n upstream commit(s)" -ForegroundColor Cyan
    $short = $head.Commit.Substring(0, 9)
    $message = "Merge upstream $($u.Url -replace '^https://github.com/|\.git$') $($u.Branch) ($short) into $($u.Prefix)/"
    # A subtree merge: the upstream tree lands under the folder, history kept.
    git -C $root merge -s subtree "-Xsubtree=$($u.Prefix)" --no-edit -m $message $head.Commit
}

$core = Get-CoreWebVersions
if ($core.Used -ne $core.Latest) {
    Write-Host "web: @stremio/stremio-core-web $($core.Used) -> $($core.Latest)" -ForegroundColor Cyan
    Push-Location (Join-Path $root 'web')
    try { corepack pnpm add --save-exact "@stremio/stremio-core-web@$($core.Latest)" } finally { Pop-Location }
    git -C $root add web/package.json web/pnpm-lock.yaml
    git -C $root commit -m "web: @stremio/stremio-core-web $($core.Latest)"
}

if (-not $SkipTests) { Invoke-AllTests }
Write-Host "`nAll upstreams merged." -ForegroundColor Green
