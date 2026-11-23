<#
  Releases Stremio Turbo x.y.z: starts the Release workflow on main's pushed
  HEAD and follows it. The workflow refuses unless CI passed on that code and
  every original project's latest code is merged; then it builds that commit
  with the version stamped, signs the update files, tags it and publishes the
  release. Installed apps update themselves. Nothing is built or tested here.

  Usage: mise run release x.y.z
#>
param([Parameter(Mandatory)] [string]$Version)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$root = Split-Path -Parent $PSScriptRoot

if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "version must be x.y.z: $Version" }
git -C $root fetch --quiet origin main
$local = (git -C $root rev-parse main).Trim()
$remote = (git -C $root rev-parse origin/main).Trim()
if ($local -ne $remote) { throw "main isn't pushed (local $($local.Substring(0, 9)), origin $($remote.Substring(0, 9))): push it first" }

gh workflow run release.yml --ref main -f "version=$Version"
Write-Host "Release v$Version of $($remote.Substring(0, 9)) started; following it..."
Start-Sleep -Seconds 5
$run = gh run list --workflow release.yml --branch main --event workflow_dispatch --limit 1 --json databaseId --jq '.[0].databaseId'
gh run watch $run --exit-status
Write-Host "Released: https://github.com/brunokinast/stremio-turbo/releases/tag/v$Version" -ForegroundColor Green
