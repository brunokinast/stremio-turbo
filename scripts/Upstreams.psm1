# The original projects each folder follows, and how to read their latest
# code, plus the media binaries the build and the shell's tests need. Used by
# sync-upstreams.ps1, check-upstreams.ps1, build.ps1, test.ps1 and CI.

$ErrorActionPreference = 'Stop'
$script:root = Split-Path -Parent $PSScriptRoot

# Every folder that tracks an original project. The shell comes in filtered
# (no binaries); the others are merged as they are.
$script:Upstreams = @(
    [pscustomobject]@{ Prefix = 'web'; Remote = 'up-web'; Url = 'https://github.com/Stremio/stremio-web.git'; Branch = 'development'; Filtered = $false }
    [pscustomobject]@{ Prefix = 'server'; Remote = 'up-server'; Url = 'https://github.com/rivulet-kodi/rivulet-engine.git'; Branch = 'main'; Filtered = $false }
    [pscustomobject]@{ Prefix = 'deps/torrent'; Remote = 'up-torrent'; Url = 'https://github.com/anacrolix/torrent.git'; Branch = 'master'; Filtered = $false }
    [pscustomobject]@{ Prefix = 'deps/go-utp'; Remote = 'up-utp'; Url = 'https://github.com/anacrolix/go-utp.git'; Branch = 'master'; Filtered = $false }
    [pscustomobject]@{ Prefix = 'shell'; Remote = $null; Url = 'https://github.com/Stremio/stremio-shell-ng.git'; Branch = 'main'; Filtered = $true }
)

# Binaries (libmpv, ffmpeg, the closed server.js and its runtime) stay out of
# the repository: build.ps1 downloads the ones we ship. The filter is
# deterministic, so the same upstream commits always give the same filtered
# commits and later merges find their common history.
$script:ShellFilter = @(
    '--invert-paths',
    '--path', 'bin/', '--path', 'bin-arm64/', '--path', 'mpv-arm64/', '--path', 'server.js',
    '--path-glob', '*.zip', '--path-glob', '*.dll',
    '--path', 'mpv.lib', '--path', 'mpv.exp', '--path', 'mpv.def'
)

function Invoke-Git {
    param([Parameter(ValueFromRemainingArguments)] [string[]]$Args)
    $PSNativeCommandUseErrorActionPreference = $false # exit codes are checked here
    & git -C $script:root @Args
    if ($LASTEXITCODE -ne 0) { throw "git $($Args -join ' ') failed ($LASTEXITCODE)" }
}

# Fetches an upstream and returns the commit to compare and merge: the remote
# branch, or for the shell the head of the filtered copy (out/cache).
function Get-UpstreamHead($u) {
    $PSNativeCommandUseErrorActionPreference = $false # exit codes are checked here
    if (-not $u.Filtered) {
        $remotes = @(& git -C $script:root remote)
        if ($remotes -notcontains $u.Remote) {
            Invoke-Git remote add $u.Remote $u.Url
            Invoke-Git config "remote.$($u.Remote).tagopt" --no-tags
            Invoke-Git remote set-url --push $u.Remote DISABLED
        }
        Invoke-Git fetch --quiet $u.Remote $u.Branch
        return [pscustomobject]@{ Ref = "$($u.Remote)/$($u.Branch)"; Repo = $null; Commit = (Invoke-Git rev-parse "$($u.Remote)/$($u.Branch)") }
    }
    $cache = Join-Path $script:root 'out/cache'
    New-Item -ItemType Directory -Force $cache | Out-Null
    $mirror = Join-Path $cache 'shell-upstream.git'
    if (Test-Path $mirror) {
        & git -C $mirror fetch --quiet origin "+refs/heads/$($u.Branch):refs/heads/$($u.Branch)"
    } else {
        & git clone --quiet --bare --no-tags --single-branch --branch $u.Branch $u.Url $mirror
    }
    if ($LASTEXITCODE -ne 0) { throw "cannot fetch $($u.Url)" }
    $filtered = Join-Path $cache 'shell-filtered'
    if (Test-Path $filtered) { Remove-Item -Recurse -Force $filtered }
    & git clone --quiet --no-local --branch $u.Branch $mirror $filtered
    if ($LASTEXITCODE -ne 0) { throw 'cannot copy the shell upstream' }
    $python = if (Get-Command python -ErrorAction SilentlyContinue) { 'python' } else { 'python3' }
    Push-Location $filtered
    try {
        & $python -m git_filter_repo --force --quiet @script:ShellFilter | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'git filter-repo failed (pip install git-filter-repo)' }
        $commit = (& git rev-parse HEAD).Trim()
    } finally { Pop-Location }
    Invoke-Git fetch --quiet $filtered "$($u.Branch):refs/upstream/shell"
    return [pscustomobject]@{ Ref = 'refs/upstream/shell'; Repo = $filtered; Commit = $commit }
}

# How many upstream commits aren't merged yet (0 = up to date).
function Get-UpstreamBehind($u, $head) {
    $PSNativeCommandUseErrorActionPreference = $false # exit codes are checked here
    & git -C $script:root merge-base --is-ancestor $head.Commit HEAD
    if ($LASTEXITCODE -eq 0) { return 0 }
    return [int](Invoke-Git rev-list --count "HEAD..$($head.Commit)")
}

# The latest @stremio/stremio-core-web on npm, and the one web/ uses.
function Get-CoreWebVersions {
    $PSNativeCommandUseErrorActionPreference = $false # exit codes are checked here
    $package = Get-Content (Join-Path $script:root 'web/package.json') -Raw | ConvertFrom-Json
    $used = $package.dependencies.'@stremio/stremio-core-web'
    $latest = (& npm view '@stremio/stremio-core-web' version).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'npm view failed' }
    [pscustomobject]@{ Used = $used; Latest = $latest }
}

# The media binaries the app ships, pinned by sha256 in binaries.json, downloaded once into
# out/cache. libmpv's zip is copied into shell/ (its build.rs links against libmpv and extracts the
# DLL from it); returns the cached paths of the bin/ files (ffmpeg, ffprobe) for the installer.
function Get-MediaBinaries {
    $cache = Join-Path $script:root 'out/cache'
    New-Item -ItemType Directory -Force $cache | Out-Null
    $manifest = Get-Content (Join-Path $PSScriptRoot 'binaries.json') -Raw | ConvertFrom-Json
    foreach ($file in $manifest.files) {
        $dest = Join-Path $cache ($file.path -replace '/', '_')
        $ok = (Test-Path $dest) -and ((Get-FileHash $dest -Algorithm SHA256).Hash -eq $file.sha256)
        if (-not $ok) {
            Write-Host "download $($file.path)"
            Invoke-WebRequest -Uri ($manifest.base + $file.path) -OutFile $dest -MaximumRetryCount 4 -RetryIntervalSec 5
            $hash = (Get-FileHash $dest -Algorithm SHA256).Hash
            if ($hash -ne $file.sha256) {
                Remove-Item $dest
                throw "$($file.path): sha256 $hash, expected $($file.sha256)"
            }
        }
        if ($file.path -like 'bin/*') { $dest }
        else { Copy-Item $dest (Join-Path $script:root "shell/$($file.path)") }
    }
}

Export-ModuleMember -Function Get-UpstreamHead, Get-UpstreamBehind, Get-CoreWebVersions, Get-MediaBinaries, Invoke-Git -Variable Upstreams
