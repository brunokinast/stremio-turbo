<#
  Development run: the shell with the web UI from the dev server
  (http://127.0.0.1:8080/, `pnpm exec webpack serve --mode development
  --config webpack.local.config.js` in web/) and a Rivulet built from server/.

  The shell runs with --development, so it starts no server itself: this
  script starts Rivulet with the installed app's settings and makes the shell
  its owner, so Rivulet exits with the shell (after finishing downloads).

  Usage: pwsh scripts/dev.ps1 [-Url http://127.0.0.1:8080/] [-Rivulet <exe>] [-Shell <exe>]
#>
param(
    [string]$Url = 'http://127.0.0.1:8080/',
    [string]$Rivulet = (Join-Path (Split-Path -Parent $PSScriptRoot) 'out\rivulet-engine.exe'),
    [string]$Shell
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if (-not $Shell) {
    $Shell = @(
        (Join-Path $root 'shell\target\x86_64-pc-windows-msvc\release\stremio-turbo.exe'),
        (Join-Path $env:LOCALAPPDATA 'Programs\Stremio Turbo\stremio-turbo.exe')
    ) | Where-Object { Test-Path $_ } | Select-Object -First 1
    if (-not $Shell) { throw 'no shell: build it (scripts/build.ps1) or install Stremio Turbo' }
}
if (-not (Test-Path $Rivulet)) { throw "no Rivulet at $Rivulet: run scripts/build.ps1 -SkipWeb -SkipShell" }
$api = 'http://127.0.0.1:11470'
$dataDir = Join-Path $env:LOCALAPPDATA 'rivulet-engine'
New-Item -ItemType Directory -Force $dataDir | Out-Null

# Stop any running app and server (downloads resume when Rivulet starts again).
Get-Process stremio-turbo, stremio-shell-ng, stremio-runtime, rivulet-engine -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 1

# The same settings the app gives its bundled server (shell/src/stremio_app/stremio_server/server.rs).
$env:BIND_ADDRESS = '127.0.0.1'
$env:HTTP_PORT = '11470'
$env:HTTPS_PORT = '0'
$env:BT_LISTEN_PORT = '49881'
$env:APP_PATH = $dataDir
$env:RIVULET_LOG_FILE = Join-Path $dataDir 'rivulet.log'
$env:RIVULET_ALLOWED_ORIGINS = 'http://127.0.0.1:8080'
$env:RIVULET_PPROF = '127.0.0.1:6060' # goroutine dumps if the server hangs (loopback only)
$ffmpeg = Split-Path $Shell
if (Test-Path (Join-Path $ffmpeg 'ffmpeg.exe')) { $env:PATH = "$ffmpeg;$env:PATH" }
elseif (Test-Path (Join-Path $root 'out\stage\ffmpeg.exe')) { $env:PATH = "$(Join-Path $root 'out\stage');$env:PATH" }

$shellProcess = Start-Process $Shell -PassThru -ArgumentList '--development', "--webui-url=$Url"
$env:RIVULET_OWNER_PID = "$($shellProcess.Id)"
Start-Process $Rivulet -WindowStyle Hidden | Out-Null
Write-Host "Shell $($shellProcess.Id) with $Url; Rivulet exits after it."
