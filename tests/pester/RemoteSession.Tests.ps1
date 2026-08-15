# Remote-session integration tests, driven by real terraform against the compiled
# provider. These exercise the provider's session_* arguments for single- and
# double-hop remote execution across every session_type (winrm / ssh / vmguest).
#
# They need REAL remote hosts and credentials, so they are parameter-driven from a
# gitignored config file (tests/pester/config/remote.tests.config.psd1, copied from
# the committed *.template.psd1). Any scenario that is not Enabled, or still has a
# 'TBD'/empty password, is reported as Skipped - so the suite is a no-op in CI and
# only lights up on a machine that can actually reach the hosts.
#
# The terraform config and probe .ps1 scripts live under tf/remote-session/.

BeforeDiscovery {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force

    # Build the -ForEach data at discovery time: one test case per session scenario
    # in the config (the DNS entry is owned by Dns.Tests.ps1, so it is excluded).
    $cfg = Get-RemoteTestConfig
    $script:Scenarios = @()
    if ($cfg) {
        foreach ($name in $cfg.Keys) {
            # 'Dns' and 'LinuxToWindowsSSH' are owned by their own suites.
            if ($name -eq 'Dns' -or $name -eq 'LinuxToWindowsSSH') { continue }
            $scn = $cfg[$name]
            if (($scn -is [hashtable]) -and $scn.ContainsKey('SessionType')) {
                $script:Scenarios += @{ Name = $name; Scenario = $scn }
            }
        }
    }
}

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'RemoteSession suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null

    $script:ConfigPath = Join-Path $PSScriptRoot 'tf/remote-session'
}

Describe 'remote session connectivity (compiled provider via terraform)' {

    It 'is configured by tests/pester/config/remote.tests.config.psd1' {
        if (-not $Scenarios -or $Scenarios.Count -eq 0) {
            Set-ItResult -Skipped -Because 'no remote.tests.config.psd1 scenarios are present (copy the template to enable)'
        }
    }

    # Discovery-time guard: Pester 6 fails the whole container when -ForEach gets
    # an empty array (Run.FailOnNullOrEmptyForEach defaults to true), so the
    # scenario tests must not be declared at all when no config is present. The
    # placeholder test above still reports the suite as skipped in that case.
    if ($Scenarios -and $Scenarios.Count -gt 0) {

        Context 'per configured scenario' {

            It 'scenario <Name> reaches the expected host(s)' -ForEach $Scenarios {
                $scn = $Scenario
                if (-not (Test-RemoteScenarioReady $scn)) {
                    Write-TFLog "scenario '$Name' not ready (disabled or password TBD) - skipping" 'SKIP'
                    Set-ItResult -Skipped -Because "scenario '$Name' is disabled or its password is still TBD"
                    return
                }

                $isDoubleHop = [bool]$scn.Hop2Host
                Write-TFLog "scenario '$Name': $($scn.SessionType) -> $($scn.SessionHost)$(if($isDoubleHop){" -> $($scn.Hop2Host)"})" 'STEP'

                $vars = ConvertTo-SessionTfvars -Scenario $scn
                $ws = New-TFWorkspace -ConfigPath $script:ConfigPath -Variables $vars
                try {
                    Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
                    $out = Get-TFOutput -Workspace $ws

                    # hop1 is always the computer the provider session landed on.
                    $out.probe.value.hop1 | Should -Not -BeNullOrEmpty
                    Write-TFLog "scenario '$Name': landed on hop1=$($out.probe.value.hop1)$(if($isDoubleHop){", hop2=$($out.probe.value.hop2)"})"

                    # The host we assert against is the *final* hop reached.
                    $reached = if ($isDoubleHop) { $out.probe.value.hop2 } else { $out.probe.value.hop1 }
                    $reached | Should -Not -BeNullOrEmpty
                    if ($scn.ExpectedHostname) {
                        $reached | Should -Match ([regex]::Escape($scn.ExpectedHostname))
                    }
                    if ($isDoubleHop) {
                        # The two hops must be genuinely different machines.
                        $out.probe.value.hop1 | Should -Not -Be $out.probe.value.hop2
                    }
                } finally {
                    Remove-TFWorkspace -Workspace $ws
                }
            }
        }
    }
}
