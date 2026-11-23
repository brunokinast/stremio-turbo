<#
  Writes the update files for a built installer: descriptor.json (its URL and
  sha256), descriptor.json.sig (ed25519, scripts/sign-descriptor.mjs) and
  update.json (what installed apps check). The only step that needs the
  private update key: UPDATE_SIGNING_KEY, or a file in UPDATE_SIGNING_KEY_FILE
  (default ~/.stremio-turbo/update-signing-key.pem). Called by build.ps1, and
  on its own by the Release workflow so no other step sees the key.

  Usage: pwsh scripts/sign-update.ps1 [-Installer out/StremioTurbo-<ver>-x64.exe]
#>
param([string]$Installer)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$root = Split-Path -Parent $PSScriptRoot
$out = Join-Path $root 'out'
$repo = 'brunokinast/stremio-turbo'

if (-not $Installer) {
    $found = @(Get-ChildItem $out -Filter 'StremioTurbo-*-x64.exe' | Where-Object Name -Match '^StremioTurbo-\d+\.\d+\.\d+-x64\.exe$')
    if ($found.Count -ne 1) { throw "expected one out/StremioTurbo-<version>-x64.exe, found $($found.Count)" }
    $Installer = $found[0].FullName
}
$name = Split-Path -Leaf $Installer
if ($name -notmatch '^StremioTurbo-(\d+\.\d+\.\d+)-x64\.exe$') { throw "not an installer name: $name" }
$version = $Matches[1]

if (-not $env:UPDATE_SIGNING_KEY -and -not $env:UPDATE_SIGNING_KEY_FILE) {
    $env:UPDATE_SIGNING_KEY_FILE = Join-Path $HOME '.stremio-turbo/update-signing-key.pem'
}
$base = "https://github.com/$repo/releases/download/v$version"
$descriptor = [ordered]@{
    version = $version
    files   = @([ordered]@{
            name     = $name
            url      = "$base/$name"
            checksum = (Get-FileHash $Installer -Algorithm SHA256).Hash.ToLower()
            os       = 'windows'
        })
}
$descriptorFile = Join-Path $out 'descriptor.json'
[IO.File]::WriteAllText($descriptorFile, ($descriptor | ConvertTo-Json -Depth 4))
node (Join-Path $PSScriptRoot 'sign-descriptor.mjs') $descriptorFile
$update = [ordered]@{ version = $version; versionDesc = "$base/descriptor.json" }
[IO.File]::WriteAllText((Join-Path $out 'update.json'), ($update | ConvertTo-Json))
Write-Host "update files: $out\update.json, descriptor.json, descriptor.json.sig"
