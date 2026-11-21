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
    # This lab's slots and tools, and the old streaming-research lab's slots while that folder exists
    # (the user's GOFLAGS may still point at its goexec).
    $labs = @($lab) + @(Join-Path $env:USERPROFILE 'Documents\git\streaming-research\lab' | Where-Object { Test-Path $_ })
    $want = @(foreach ($l in $labs) { 0..15 | ForEach-Object { Join-Path $l ('testbin\slots\slot{0:D2}.exe' -f $_) } }) +
            @(Get-ChildItem -Path (Join-Path $lab 'transport') -Filter *.exe | ForEach-Object FullName) +
            @(
                # The benchmark's libtorrent reference client runs on Python 3.13 (research/lab/bench/ltclient.py).
                (Join-Path $env:LOCALAPPDATA 'Programs\Python\Python313\python.exe'),
                # The libtorrent engine prototype (plan 16 Part E) is built to this fixed path.
                (Join-Path $lab 'transport\rivulet-lt.exe')
            )
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
