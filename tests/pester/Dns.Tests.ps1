# DNS integration suite, driven by real terraform against the compiled provider.
# It creates a real DNS A record, modifies it, rebuilds the terraform state from the
# TF file via `terraform import`, and finally destroys the record - the full
# create -> update -> rebuild -> delete lifecycle against a live Windows DNS server.
#
# It needs a reachable DNS server and credentials, so it is parameter-driven from
# the gitignored config (tests/pester/config/remote.tests.config.psd1, 'Dns' entry,
# copied from the committed template). When that entry is absent, disabled, or still
# has a 'TBD' password, the suite is reported as Skipped and is a no-op in CI.
#
# The terraform config and DnsServer-based .ps1 scripts live under tf/dns-record/.

BeforeDiscovery {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    $cfg = Get-RemoteTestConfig
    $script:DnsScenario = if ($cfg) { $cfg['Dns'] } else { $null }
    $script:DnsReady = [bool]($script:DnsScenario -and (Test-RemoteScenarioReady $script:DnsScenario))
}

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'Dns suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null
    $script:ConfigPath = Join-Path $PSScriptRoot 'tf/dns-record'

    # Re-read the scenario at runtime and turn it into string tfvars.
    $cfg = Get-RemoteTestConfig
    $script:Scn = if ($cfg) { $cfg['Dns'] } else { $null }

    function script:Get-DnsTfvars {
        param([string]$Ipv4)
        $s = $script:Scn
        $vars = @{
            session_type           = if ($s.SessionType) { $s.SessionType } else { 'winrm' }
            session_host           = $s.SessionHost
            session_username       = $s.SessionUsername
            session_password       = $s.SessionPassword
            session_authentication = if ($s.SessionAuthentication) { $s.SessionAuthentication } else { 'Negotiate' }
            dns_server             = if ($s.DnsServer) { $s.DnsServer } else { 'localhost' }
            zone                   = $s.Zone
            record_name            = $s.RecordName
            ipv4_address           = $Ipv4
        }
        if ($s.ContainsKey('SessionUseSSL')) {
            $vars['session_use_ssl'] = if ($s.SessionUseSSL) { 'true' } else { 'false' }
        }
        return $vars
    }
}

Describe 'DNS A-record lifecycle (compiled provider via terraform)' {

    It 'is configured by the Dns entry in remote.tests.config.psd1' {
        if (-not $DnsReady) {
            Set-ItResult -Skipped -Because 'no ready Dns scenario (absent, disabled, or password still TBD)'
        }
    }

    Context 'create -> update -> rebuild -> destroy' -Skip:(-not $DnsReady) {

        BeforeAll {
            $script:ws = New-TFWorkspace -ConfigPath $script:ConfigPath -Variables (Get-DnsTfvars -Ipv4 $script:Scn.Ipv4Address)
            Write-TFLog "DNS create: $($script:Scn.RecordName).$($script:Scn.Zone) -> $($script:Scn.Ipv4Address)" 'STEP'
            Invoke-TF -Workspace $script:ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $script:created = (Get-TFOutput -Workspace $script:ws).record.value
        }

        AfterAll {
            if ($script:ws) { Remove-TFWorkspace -Workspace $script:ws }
        }

        It 'creates the A record with the configured address' {
            $script:created.record_name  | Should -Be $script:Scn.RecordName
            $script:created.ipv4_address | Should -Be $script:Scn.Ipv4Address
            $script:created.id           | Should -Match ([regex]::Escape("/$($script:Scn.Zone)/$($script:Scn.RecordName)"))
        }

        It 'updates the A record to a new address in place' {
            Write-TFLog "DNS update -> $($script:Scn.Ipv4AddressUpdated)" 'STEP'
            $vars = Get-DnsTfvars -Ipv4 $script:Scn.Ipv4AddressUpdated
            $lines = foreach ($k in $vars.Keys) { "$k = `"$($vars[$k])`"" }
            Set-Content -Path (Join-Path $script:ws.Dir 'terraform.tfvars') -Value ($lines -join "`n") -Encoding UTF8

            Invoke-TF -Workspace $script:ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $out = (Get-TFOutput -Workspace $script:ws).record.value
            $out.ipv4_address | Should -Be $script:Scn.Ipv4AddressUpdated
        }

        It 'rebuilds the lost state from the TF file via import' {
            $id = (Get-TFOutput -Workspace $script:ws).record.value.id
            Write-TFLog "DNS state-rebuild: deleting state, re-importing id=$id" 'STEP'

            Get-ChildItem -Path $script:ws.Dir -Filter 'terraform.tfstate*' | Remove-Item -Force
            Test-Path (Join-Path $script:ws.Dir 'terraform.tfstate') | Should -BeFalse

            Invoke-TF -Workspace $script:ws -Arguments @('import', '-no-color', 'powershell_script.record', $id) | Out-Null
            (Invoke-TF -Workspace $script:ws -Arguments @('state', 'list', '-no-color') | Out-String) | Should -Match 'powershell_script\.record'

            # The resource is back in state under its original, self-describing id.
            (Invoke-TF -Workspace $script:ws -Arguments @('state', 'show', '-no-color', 'powershell_script.record') | Out-String) |
                Should -Match ([regex]::Escape($id))

            # Reconcile config (scripts/input_data) back into state; the rebuild is
            # complete when a follow-up plan is clean and the record matches config.
            Invoke-TF -Workspace $script:ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $clean = Invoke-TFExit -Workspace $script:ws -Arguments @('plan', '-no-color', '-detailed-exitcode')
            $clean.ExitCode | Should -Be 0
            (Get-TFOutput -Workspace $script:ws).record.value.ipv4_address | Should -Be $script:Scn.Ipv4AddressUpdated
        }

        It 'destroys the A record' {
            Write-TFLog 'DNS destroy' 'STEP'
            Invoke-TF -Workspace $script:ws -Arguments @('destroy', '-auto-approve', '-no-color') | Out-Null
            # Re-reading after destroy: a plan now wants to re-create it (state empty).
            $plan = Invoke-TFExit -Workspace $script:ws -Arguments @('plan', '-no-color', '-detailed-exitcode')
            $plan.ExitCode | Should -Be 2
        }
    }
}
