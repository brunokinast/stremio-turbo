<#
  Starts Stremio with this fork's web UI and the Rivulet Engine streaming server
  (open-source replacement for Stremio's closed server.js:
  https://github.com/rivulet-kodi/rivulet-engine).

  The shell runs with --development, so it doesn't start its own server.js; Rivulet serves
  the same API on 127.0.0.1:11470 instead. When the shell exits, Rivulet is stopped, unless
  downloads (download ahead) are still running: then it is left to finish them and exits by
  itself (exit-when-done). A Rivulet still finishing downloads is adopted on the next start.

  Usage: pwsh -WindowStyle Hidden -File scripts/start-stremio.ps1 [-Url http://127.0.0.1:8080/]
#>
param(
    [string]$Url = 'http://127.0.0.1:8080/',
    [string]$Rivulet = (Join-Path $env:USERPROFILE 'Tools\rivulet\rivulet-engine.exe')
)

$stremioDir = Join-Path $env:LOCALAPPDATA 'Programs\Stremio'
$dataDir = Join-Path $env:LOCALAPPDATA 'rivulet-engine'
$peerPort = 49881  # 6881 is already mapped on the router by another device

$api = 'http://127.0.0.1:11470'

function Get-PendingDownloads {
    try {
        # Assigned first: Invoke-RestMethod passes a JSON array down the pipeline as one object.
        $all = Invoke-RestMethod "$api/downloads" -TimeoutSec 5
        @($all | Where-Object { $_.state -in 'queued', 'downloading' })
    } catch { @() }
}

function Set-ExitWhenDone([bool]$On) {
    Invoke-RestMethod -Method Post "$api/downloads/exit-when-done" -TimeoutSec 5 `
        -ContentType 'application/json' -Body (@{ on = $On } | ConvertTo-Json) | Out-Null
}

# This launcher is now the one in charge of the server. Recorded before the old shell is stopped:
# the launcher that started it wakes when it exits, and must see it no longer owns the server
# (otherwise it stops the server this one is about to adopt).
New-Item -ItemType Directory -Force $dataDir | Out-Null
$ownerFile = Join-Path $dataDir 'launcher.pid'
Set-Content -Path $ownerFile -Value $PID

# The shell is single instance and hides to tray: it must be gone first. Our own Rivulet,
# left running to finish downloads, is adopted; any other server is stopped.
Get-Process stremio-shell-ng, stremio-runtime -ErrorAction SilentlyContinue | Stop-Process -Force
$server = $null
foreach ($p in @(Get-Process rivulet-engine -ErrorAction SilentlyContinue)) {
    if (-not $server -and $p.Path -eq $Rivulet) {
        try { Set-ExitWhenDone $false; $server = $p; continue } catch { }
    }
    Stop-Process -Id $p.Id -Force
}
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

if (-not $server) {
    # Keep the previous run's logs (a server that exited or crashed) instead of overwriting them.
    foreach ($log in 'rivulet.log', 'rivulet.err.log') {
        $path = Join-Path $dataDir $log
        if (Test-Path $path) { Move-Item -Force $path ($path -replace '\.log$', '.prev.log') }
    }
    $server = Start-Process $Rivulet -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $dataDir 'rivulet.log') `
        -RedirectStandardError (Join-Path $dataDir 'rivulet.err.log')
}

$shell = Start-Process (Join-Path $stremioDir 'stremio-shell-ng.exe') -PassThru `
    -ArgumentList '--development', "--webui-url=$Url"

$shell.WaitForExit()
if ((Get-Content $ownerFile -ErrorAction SilentlyContinue) -ne "$PID") {
    return  # a newer launcher took the server over
}
if ((Get-PendingDownloads).Count -gt 0) {
    try { Set-ExitWhenDone $true; return } catch { }
}
Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
