<#
  Starts Stremio with this fork's web UI and the Rivulet Engine streaming server
  (open-source replacement for Stremio's closed server.js:
  https://github.com/rivulet-kodi/rivulet-engine).

  The shell runs with --development, so it doesn't start its own server.js; Rivulet serves
  the same API on 127.0.0.1:11470 instead. When the shell exits, Rivulet is stopped.

  Usage: pwsh -WindowStyle Hidden -File scripts/start-stremio.ps1 [-Url http://127.0.0.1:8080/]
#>
param(
    [string]$Url = 'http://127.0.0.1:8080/',
    [string]$Rivulet = (Join-Path $env:USERPROFILE 'Tools\rivulet\rivulet-engine.exe')
)

$stremioDir = Join-Path $env:LOCALAPPDATA 'Programs\Stremio'
$dataDir = Join-Path $env:LOCALAPPDATA 'rivulet-engine'
$peerPort = 49881  # 6881 is already mapped on the router by another device

# Both the shell (single instance, hides to tray) and any server must be gone first.
Get-Process stremio-shell-ng, stremio-runtime, rivulet-engine -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 1

New-Item -ItemType Directory -Force $dataDir | Out-Null

# Rivulet configuration (docs/CONFIGURATION.md in its repo).
$env:BIND_ADDRESS = '127.0.0.1'                 # API is unauthenticated: loopback only
$env:HTTP_PORT = '11470'
$env:HTTPS_PORT = '0'
$env:BT_LISTEN_PORT = "$peerPort"               # accept incoming peers
$env:APP_PATH = $dataDir
$env:RIVULET_ALLOWED_ORIGINS = 'http://127.0.0.1:8080,https://brunokinast.github.io'
$env:PATH = "$stremioDir;$env:PATH"             # ffmpeg/ffprobe shipped with Stremio

$server = Start-Process $Rivulet -WindowStyle Hidden -PassThru `
    -RedirectStandardOutput (Join-Path $dataDir 'rivulet.log') `
    -RedirectStandardError (Join-Path $dataDir 'rivulet.err.log')

$shell = Start-Process (Join-Path $stremioDir 'stremio-shell-ng.exe') -PassThru `
    -ArgumentList '--development', "--webui-url=$Url"

$shell.WaitForExit()
Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
