<#
  Exits 0 when CI passed on a commit's code: CI's run on that commit succeeded,
  or (as CI skips docs-only pushes) the newest successful CI run on main is an
  ancestor and every change since then is in a path CI ignores. Used by the
  Release workflow's gate; needs gh (GH_TOKEN) and the history (fetch-depth 0).

  Usage: pwsh scripts/ci-passed.ps1 <commit sha>
#>
param([Parameter(Mandatory)] [string]$Sha)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true

# The same paths as ci.yml's paths-ignore: keep the two lists equal.
$ignored = '^docs/', '^research/', '\.md$', '^scripts/readme-images\.mjs$'

$runs = gh run list --workflow ci.yml --branch main --limit 100 --json headSha,status,conclusion | ConvertFrom-Json
$own = @($runs | Where-Object headSha -eq $Sha)
if ($own) {
    if ($own | Where-Object status -ne 'completed') { throw "CI is still running on ${Sha}: release once it passes" }
    if ($own | Where-Object conclusion -eq 'success') { Write-Host "CI passed on $Sha"; exit 0 }
    throw "CI didn't pass on ${Sha} ($(($own | ForEach-Object conclusion) -join ', '))"
}

$green = $runs | Where-Object { $_.status -eq 'completed' -and $_.conclusion -eq 'success' } | Select-Object -First 1
if (-not $green) { throw 'no successful CI run on main' }
$PSNativeCommandUseErrorActionPreference = $false
git merge-base --is-ancestor $green.headSha $Sha
if ($LASTEXITCODE -ne 0) { throw "the last green CI commit $($green.headSha) isn't an ancestor of $Sha" }
$PSNativeCommandUseErrorActionPreference = $true
$changed = @(git diff --name-only $green.headSha $Sha)
$tested = @($changed | Where-Object { $f = $_; -not ($ignored | Where-Object { $f -match $_ }) })
if ($tested) { throw "changed since CI last passed ($($green.headSha)) and not tested: $($tested -join ', ')" }
Write-Host "CI passed on $($green.headSha); since then only docs changed ($($changed.Count) files)"
