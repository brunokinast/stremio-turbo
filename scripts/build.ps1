<#
  Builds Stremio Turbo: the web UI, the Rivulet server and the shell, then the
  Windows installer and the signed update files.

  Output (out/):
    stage/                        everything the app installs
    StremioTurbo-<ver>-x64.exe    the installer
    update.json                   what the app's updater checks (latest release)
    descriptor.json(.sig)         the installer's URL and sha256, signed

  The version is shell/Cargo.toml's. Signing needs the private update key in
  UPDATE_SIGNING_KEY, or a file in UPDATE_SIGNING_KEY_FILE (default
  ~/.stremio-turbo/update-signing-key.pem); without it the update files are
  skipped (-NoSign does that on purpose).

  Usage: pwsh scripts/build.ps1 [-NoSign] [-SkipWeb] [-SkipServer] [-SkipShell]
#>
param(
    [switch]$NoSign,
    [switch]$SkipWeb,
    [switch]$SkipServer,
    [switch]$SkipShell
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$root = Split-Path -Parent $PSScriptRoot
$out = Join-Path $root 'out'
$stage = Join-Path $out 'stage'
$cache = Join-Path $out 'cache'
$repo = 'brunokinast/stremio-turbo'

function Step($name) { Write-Host "`n== $name" -ForegroundColor Cyan }

$version = (Select-String -Path (Join-Path $root 'shell/Cargo.toml') -Pattern '^version = "(.+)"' |
    Select-Object -First 1).Matches[0].Groups[1].Value
$commit = (git -C $root rev-parse --short HEAD).Trim()
Write-Host "Stremio Turbo $version ($commit)"

New-Item -ItemType Directory -Force $out, $cache | Out-Null
if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
New-Item -ItemType Directory -Force $stage | Out-Null

# Media binaries, pinned by sha256 (scripts/binaries.json).
Step 'Media binaries'
$manifest = Get-Content (Join-Path $PSScriptRoot 'binaries.json') -Raw | ConvertFrom-Json
foreach ($file in $manifest.files) {
    $dest = Join-Path $cache ($file.path -replace '/', '_')
    $ok = (Test-Path $dest) -and ((Get-FileHash $dest -Algorithm SHA256).Hash -eq $file.sha256)
    if (-not $ok) {
        Write-Host "download $($file.path)"
        Invoke-WebRequest -Uri ($manifest.base + $file.path) -OutFile $dest
        $hash = (Get-FileHash $dest -Algorithm SHA256).Hash
        if ($hash -ne $file.sha256) {
            Remove-Item $dest
            throw "$($file.path): sha256 $hash, expected $($file.sha256)"
        }
    }
    if ($file.path -like 'bin/*') {
        Copy-Item $dest (Join-Path $stage (Split-Path -Leaf $file.path))
    } else {
        # The shell's build.rs links against libmpv and extracts the DLL from this zip.
        Copy-Item $dest (Join-Path $root "shell/$($file.path)")
    }
}

if (-not $SkipWeb) {
    Step 'Web UI'
    Push-Location (Join-Path $root 'web')
    try {
        corepack pnpm install --frozen-lockfile
        corepack pnpm run build
    } finally { Pop-Location }
}
$webBuild = Join-Path $root 'web/build'
if (-not (Test-Path (Join-Path $webBuild 'index.html'))) { throw 'web/build is missing: build the web UI' }
Copy-Item -Recurse $webBuild (Join-Path $stage 'web')

$serverExe = Join-Path $out 'rivulet-engine.exe'
if (-not $SkipServer) {
    Step 'Server'
    Push-Location (Join-Path $root 'server')
    try {
        $env:GOWORK = 'off'
        $env:CGO_ENABLED = '0'
        $date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
        go build -trimpath -ldflags "-s -w -X main.buildVersion=turbo-$version -X main.buildCommit=$commit -X main.buildDate=$date" -o $serverExe ./cmd/rivulet-engine
    } finally { Pop-Location }
}
Copy-Item $serverExe $stage

$shellExe = Join-Path $root 'shell/target/x86_64-pc-windows-msvc/release/stremio-turbo.exe'
if (-not $SkipShell) {
    Step 'Shell'
    Push-Location (Join-Path $root 'shell')
    try {
        $cargo = Get-Command cargo -ErrorAction SilentlyContinue
        if (-not $cargo) { $env:PATH = "$HOME\.cargo\bin;$env:PATH" }
        cargo build --release --target x86_64-pc-windows-msvc
    } finally { Pop-Location }
}
Copy-Item $shellExe $stage
Copy-Item (Join-Path $root 'shell/libmpv-2.dll') $stage

Step 'Licences'
$licenses = New-Item -ItemType Directory -Force (Join-Path $stage 'licenses')
Copy-Item (Join-Path $root 'web/LICENSE.md') (Join-Path $licenses 'stremio-web.LICENSE.md')
Copy-Item (Join-Path $root 'server/LICENSE') (Join-Path $licenses 'rivulet-engine.LICENSE')
Copy-Item (Join-Path $root 'deps/torrent/LICENSE') (Join-Path $licenses 'anacrolix-torrent.LICENSE')
Copy-Item (Join-Path $root 'deps/go-utp/LICENSE') (Join-Path $licenses 'anacrolix-go-utp.LICENSE')
@"
Stremio Turbo $version ($commit)
Source: https://github.com/$repo

Bundled third-party binaries, as shipped by Stremio's Windows shell
(https://github.com/Stremio/stremio-shell-ng):
- libmpv-2.dll: mpv (https://mpv.io), LGPL-2.1+ / GPL-2.0+ depending on the build.
- ffmpeg.exe, ffprobe.exe, av*.dll, postproc, swresample, swscale: FFmpeg
  (https://ffmpeg.org), LGPL-2.1+ / GPL-2.0+ depending on the build.
- vcruntime140*.dll: Microsoft Visual C++ runtime (redistributable).
"@ | Set-Content -Encoding utf8 (Join-Path $licenses 'THIRD-PARTY.txt')

Step 'Installer'
$iscc = @(
    "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe",
    "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
    "$env:ProgramFiles\Inno Setup 6\ISCC.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $iscc) { throw 'Inno Setup 6 (ISCC.exe) not found' }
# Stremio's installer lists Icelandic, which some Inno Setup releases don't ship.
$icelandic = Join-Path (Split-Path $iscc) 'Languages\Icelandic.isl'
if (-not (Test-Path $icelandic)) {
    $isl = Join-Path $cache 'Icelandic.isl'
    Invoke-WebRequest -OutFile $isl -Uri 'https://raw.githubusercontent.com/jrsoftware/issrc/b4f695abf8a319ec18e61f23d5a98bf79c5d8504/Files/Languages/Icelandic.isl'
    if ((Get-FileHash $isl -Algorithm SHA256).Hash -ne 'BD971880A17E257615F37AC355523ECF755066C2F6F70777522F8A96F5D3A5E8') {
        throw 'Icelandic.isl: unexpected sha256'
    }
    Copy-Item $isl $icelandic
}
& $iscc /Q (Join-Path $root 'shell/setup/Stremio.iss')
$installer = Join-Path $out "StremioTurbo-$version-x64.exe"
if (-not (Test-Path $installer)) { throw "installer not produced: $installer" }
Write-Host "installer: $installer"

Step 'Update files'
$keyFile = if ($env:UPDATE_SIGNING_KEY_FILE) { $env:UPDATE_SIGNING_KEY_FILE } else { Join-Path $HOME '.stremio-turbo/update-signing-key.pem' }
if ($NoSign -or (-not $env:UPDATE_SIGNING_KEY -and -not (Test-Path $keyFile))) {
    Write-Host 'skipped (no signing key)'
    return
}
$base = "https://github.com/$repo/releases/download/v$version"
$descriptor = [ordered]@{
    version = $version
    files   = @([ordered]@{
            name     = Split-Path -Leaf $installer
            url      = "$base/$(Split-Path -Leaf $installer)"
            checksum = (Get-FileHash $installer -Algorithm SHA256).Hash.ToLower()
            os       = 'windows'
        })
}
$descriptorFile = Join-Path $out 'descriptor.json'
[IO.File]::WriteAllText($descriptorFile, ($descriptor | ConvertTo-Json -Depth 4))
$env:UPDATE_SIGNING_KEY_FILE = $keyFile
node (Join-Path $PSScriptRoot 'sign-descriptor.mjs') $descriptorFile
$update = [ordered]@{ version = $version; versionDesc = "$base/descriptor.json" }
[IO.File]::WriteAllText((Join-Path $out 'update.json'), ($update | ConvertTo-Json))
Write-Host "update files: $out\update.json, descriptor.json, descriptor.json.sig"
