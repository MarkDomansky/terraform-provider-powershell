# Linux -> Windows remoting test, driven by real terraform against the compiled
# provider. Proves the module can open a remote PowerShell session from a LINUX
# origin INTO a Windows host. PowerShell on Linux has no WinRM client, so that leg
# must be SSH: the Windows target runs OpenSSH with the PowerShell subsystem and
# authenticates by key file (SSH remoting has no non-interactive password).
#
# Rather than requiring the whole Pester harness to run on Linux, this suite drives
# the Linux origin remotely as a DOUBLE HOP from the Windows test host:
#   hop0: Windows harness --ssh--> hop0linux (Debian)   [provider session]
#   hop1: Debian          --ssh--> Windows target        [probe second hop]
# The probe reports the hostname AND OS platform of each hop, so the test can assert
# the origin really is Linux (platform1 = 'Unix') and that it reached a Windows
# machine (platform2 = 'Win32NT').
#
# It is parameter-driven from the gitignored 'LinuxToWindowsSSH' scenario in
# tests/pester/config/remote.tests.config.psd1 (Hop2Type='ssh'). With no/unfilled
# config the suite is reported as Skipped, so it is a no-op in CI and only lights up
# where the hosts and keys are actually reachable. It reuses the probe under
# tf/remote-session/.

BeforeDiscovery {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    $cfg = Get-RemoteTestConfig
    $script:Scn = if ($cfg) { $cfg['LinuxToWindowsSSH'] } else { $null }
    $script:Ready = [bool]($script:Scn -and (Test-RemoteScenarioReady $script:Scn))
}

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'LinuxToWindows suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null
    $script:ConfigPath = Join-Path $PSScriptRoot 'tf/remote-session'
    $cfg = Get-RemoteTestConfig
    $script:Scn = if ($cfg) { $cfg['LinuxToWindowsSSH'] } else { $null }
}

Describe 'Linux -> Windows remoting over SSH (compiled provider via terraform)' {

    It 'has a ready LinuxToWindowsSSH scenario' {
        if (-not $Ready) {
            Set-ItResult -Skipped -Because 'no ready LinuxToWindowsSSH scenario (absent, disabled, or no key files)'
        }
    }

    Context 'Linux origin (hop0linux) -> Windows over SSH' -Skip:(-not $Ready) {

        BeforeAll {
            $expectedPlatform = if ($script:Scn.ExpectedPlatform) { $script:Scn.ExpectedPlatform } else { 'Win32NT' }
            $script:ExpectedPlatform = $expectedPlatform
            Write-TFLog "harness -> ssh -> $($script:Scn.SessionHost) (Linux) -> ssh -> $($script:Scn.Hop2Host) (Windows)" 'STEP'

            $script:ws = New-TFWorkspace -ConfigPath $script:ConfigPath -Variables (ConvertTo-SessionTfvars -Scenario $script:Scn)
            Invoke-TF -Workspace $script:ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $script:probe = (Get-TFOutput -Workspace $script:ws).probe.value
            Write-TFLog "hop1(Linux)=$($script:probe.hop1)/$($script:probe.platform1) -> hop2(Windows)=$($script:probe.hop2)/$($script:probe.platform2)"
        }

        AfterAll {
            if ($script:ws) { Remove-TFWorkspace -Workspace $script:ws }
        }

        It 'the origin hop (hop0linux) is Linux' {
            # platform1 is reported from hop1 - the box the Windows->Debian SSH landed
            # on. 'Unix' proves the machine reaching into Windows is itself Linux.
            $script:probe.platform1 | Should -Be 'Unix'
        }

        It 'reaches the Windows target from the Linux hop' {
            $script:probe.hop2 | Should -Not -BeNullOrEmpty
            if ($script:Scn.ExpectedHostname) {
                $script:probe.hop2 | Should -Match ([regex]::Escape($script:Scn.ExpectedHostname))
            }
        }

        It 'confirms the reached host is Windows' {
            # The final probe ran ON the Windows target; Win32NT proves a Linux client
            # reached a Windows host over SSH.
            $script:probe.platform2 | Should -Be $script:ExpectedPlatform
        }
    }
}
