<#
  Releases a new Stremio Turbo version. Refuses unless every original
  project's latest code is merged (check-upstreams.ps1), the working tree is
  clean on main, and all tests pass. Then sets the version, commits, tags, and
  with -Push pushes main and the tag: the release workflow builds the
  installer and publishes it, and installed apps update themselves.

  Usage: pwsh scripts/release.ps1 -Version 1.2.0 [-Push] [-SkipTests]
#>
param(
    [Parameter(Mandatory)] [string]$Version,
    [switch]$Push,
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
Import-Module (Join-Path $PSScriptRoot 'Upstreams.psm1') -Force
$root = Split-Path -Parent $PSScriptRoot

if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "version must be x.y.z: $Version" }
if ((git -C $root branch --show-current).Trim() -ne 'main') { throw 'release from main' }
if (git -C $root status --porcelain --untracked-files=no) { throw 'the working tree has uncommitted changes' }
if (git -C $root tag --list "v$Version") { throw "v$Version already exists" }

& pwsh -NoProfile -File (Join-Path $PSScriptRoot 'check-upstreams.ps1')
if ($LASTEXITCODE -ne 0) { throw 'not released: an upstream has unmerged changes (run scripts/sync-upstreams.ps1)' }

if (-not $SkipTests) { Invoke-AllTests }

$cargo = Join-Path $root 'shell/Cargo.toml'
$text = [IO.File]::ReadAllText($cargo)
$text = [regex]::new('(?m)^version = "[^"]+"').Replace($text, "version = `"$Version`"", 1)
[IO.File]::WriteAllText($cargo, $text)
if (-not (Get-Command cargo -ErrorAction SilentlyContinue)) { $env:PATH = "$HOME\.cargo\bin;$env:PATH" }
Push-Location (Join-Path $root 'shell')
try { cargo update --workspace --offline } finally { Pop-Location }
git -C $root add shell/Cargo.toml shell/Cargo.lock
git -C $root commit -m "Release v$Version"
git -C $root tag -a "v$Version" -m "Stremio Turbo $Version"
Write-Host "Tagged v$Version." -ForegroundColor Green

if ($Push) {
    git -C $root push origin main "v$Version"
    Write-Host 'Pushed: the release workflow builds and publishes it.'
} else {
    Write-Host "Push with: git push origin main v$Version"
}
