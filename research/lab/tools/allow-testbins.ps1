# Keeps the firewall quiet for lab work. Run elevated (one UAC):
#   Start-Process pwsh -Verb RunAs -ArgumentList '-NoProfile','-File','<this file>'
# - Allows goexec's 16 fixed slots (every `go test` / `go run` binary runs from one of them, through
#   GOFLAGS=-exec=...\lab\tools\goexec.exe) and the lab tools in lab\transport. Rules match exact
#   paths and may name files that don't exist yet.
# - Removes rules for binaries that no longer exist: the group's own leftovers and duplicates, and the
#   per-run rules Windows prompted for under %TEMP%\go-build* before goexec existed.
$ErrorActionPreference = 'Continue'
$lab = Split-Path -Parent $PSScriptRoot
$log = Join-Path $lab 'testbin\allow-testbins.log'
Start-Transcript -Path $log -Force | Out-Null
try {
    $want = @(0..15 | ForEach-Object { Join-Path $lab ('testbin\slots\slot{0:D2}.exe' -f $_) }) +
            @(Get-ChildItem -Path (Join-Path $lab 'transport') -Filter *.exe | ForEach-Object FullName)
    $wantSet = @{}; foreach ($w in $want) { $wantSet[$w.ToLower()] = $true }

    # One bulk read of every rule and every program filter, joined by InstanceID: a lookup per rule
    # took minutes on a machine with hundreds of rules.
    Write-Host 'Reading firewall rules...'
    $byId = @{}
    foreach ($r in Get-NetFirewallRule) { $byId[$r.InstanceID] = $r }
    $rules = @(foreach ($f in Get-NetFirewallApplicationFilter -All) {
        if ($f.Program -and $f.Program -ne 'Any' -and $byId.ContainsKey($f.InstanceID)) {
            [pscustomobject]@{ Rule = $byId[$f.InstanceID]; Program = $f.Program }
        }
    })
    Write-Host "$($rules.Count) program rules read"
    $stale = @(); $have = @{}
    foreach ($r in $rules) {
        $p = $r.Program.ToLower()
        $ours = $r.Rule.Group -eq 'streaming-lab'
        if ($p -like '*\temp\go-build*' -or ($p -like '*\streaming-research\lab\testbin\*' -and -not $wantSet[$p]) -or
            ($ours -and -not $wantSet[$p]) -or ($ours -and $have[$p])) {
            $stale += $r.Rule
        } elseif ($ours) {
            $have[$p] = $true
        }
    }
    Write-Host "Removing $($stale.Count) stale rules..."
    foreach ($s in $stale) { Remove-NetFirewallRule -Name $s.Name }
    $added = 0
    foreach ($e in $want) {
        if ($have[$e.ToLower()]) { continue }
        New-NetFirewallRule -DisplayName "streaming-lab: $(Split-Path -Leaf $e)" -Group 'streaming-lab' -Direction Inbound `
            -Action Allow -Program $e -Profile Any | Out-Null
        $added++
    }
    "RESULT: $added added, $($stale.Count) stale removed, $($want.Count) allowed"
} finally {
    Stop-Transcript | Out-Null
}
