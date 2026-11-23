<#
  Every folder's checks, or some areas': web, go (= server, torrent, utp, the
  three Go modules, which CI runs in parallel) or shell. Run through mise
  (`mise run test`, `mise run test:go`), which puts the pinned toolchains on
  PATH; CI runs the same script. Stops at the first failure.

  Usage: pwsh scripts/test.ps1 [web|go|server|torrent|utp|shell ...]
#>
param([ValidateSet('web', 'go', 'server', 'torrent', 'utp', 'shell')] [string[]]$Area = @('web', 'go', 'shell'))
if ('go' -in $Area) { $Area = @($Area | Where-Object { $_ -ne 'go' }) + 'server', 'torrent', 'utp' }

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$root = Split-Path -Parent $PSScriptRoot

function In($dir, [scriptblock]$body) {
    Push-Location (Join-Path $root $dir)
    try { & $body } finally { Pop-Location }
}

# gofmt -s must list nothing (deps/torrent is skipped: upstream leaves some files unformatted).
function Assert-Gofmt($dir) {
    $bad = In $dir { gofmt -s -l . }
    if ($bad) { throw "gofmt -s: $dir has unformatted files:`n$($bad -join "`n")" }
}

if ('web' -in $Area) {
    In web {
        pnpm install --frozen-lockfile
        pnpm run lint
        pnpm test
        pnpm run build
    }
}

$env:GOWORK = 'off'
if ('server' -in $Area) {
    Assert-Gofmt server
    In server {
        # cmd/librivulet-engine is a cgo library (built by Rivulet's own release tooling, not
        # here); the symlink test needs the privilege to create symlinks, which only an elevated
        # or developer-mode Windows has.
        $packages = @(go list ./... | Where-Object { $_ -notmatch '/cmd/librivulet-engine$' })
        $admin = $IsWindows -and ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
        $skip = if ($admin -or -not $IsWindows) { @() } else { @('-skip', 'TestArchiveResolveLocalPath_SymlinkEscape') }
        go vet @packages
        go test -p 1 @skip @packages
    }
}
if ('utp' -in $Area) {
    Assert-Gofmt deps/go-utp
    In deps/go-utp {
        go vet ./...
        go test ./...
    }
}
if ('torrent' -in $Area) {
    In deps/torrent { go test . }
}

if ('shell' -in $Area) {
    Import-Module (Join-Path $PSScriptRoot 'Upstreams.psm1') -Force
    Get-MediaBinaries | Out-Null # the shell's build.rs needs libmpv's zip
    In shell { cargo test --release --target x86_64-pc-windows-msvc }
}
