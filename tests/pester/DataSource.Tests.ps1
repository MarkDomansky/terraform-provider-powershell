# End-to-end tests for the powershell_script data source, driven by real
# terraform against the compiled provider: merged (sensitive) input, provider
# global visibility, the reserved 'sensitive' output split, and the
# empty-output contract.

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'DataSource suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null

    $script:TfDir = Join-Path $PSScriptRoot 'tf'
}

Describe 'powershell_script data source (compiled provider via terraform)' {

    It 'reads data with merged input, provider globals, and the sensitive output split' {
        Write-TFLog 'data source read + sensitive split + empty output' 'STEP'
        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir 'data-source')
        try {
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $out = Get-TFOutput -Workspace $ws

            # input_data and sensitive_input_data arrived merged in $InputData.
            $out.info.value.combined | Should -Be 'app-hush'
            # The startup script's global was visible (same persistent runspace).
            $out.info.value.farm | Should -Be 'farm-a'
            # Data source scripts run as the "read" action.
            $out.info.value.action | Should -Be 'read'
            # The reserved 'sensitive' key was stripped from output_data...
            $out.info.value.PSObject.Properties.Name | Should -Not -Contain 'sensitive'
            # ...and surfaced in sensitive_output_data instead.
            $out.info_secret.value.secret_suffix | Should -Be 'hush'
            $out.info_secret.sensitive | Should -BeTrue

            # A script that emits nothing yields empty JSON output, not an error.
            $out.empty.value | Should -Be '{}'
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
    }
}
