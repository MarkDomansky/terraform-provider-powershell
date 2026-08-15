# End-to-end tests for the sensitive data channels, driven by real terraform
# against the compiled provider:
#   - sensitive_provider_data -> $global:ProviderData.SensitiveData;
#   - sensitive_input_data merges into $InputData and is redacted from output;
#   - the reserved 'sensitive' output key splits into sensitive_output_data.

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'SensitiveData suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null

    $script:TfDir = Join-Path $PSScriptRoot 'tf'
    $script:Secret = 'hunter2-super-secret'
}

Describe 'powershell_script sensitive data (compiled provider via terraform)' {

    It 'routes sensitive input/output through the sensitive attributes without leaking to CLI output' {
        Write-TFLog 'sensitive input merge + sensitive output split' 'STEP'
        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir 'sensitive-data') -Variables @{ secret = $script:Secret }
        try {
            $applyOutput = Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color')

            # The secret must never appear in the apply output: the sensitive
            # attributes are redacted and the derived token lives only in
            # sensitive_output_data.
            ($applyOutput -join "`n") | Should -Not -Match ([regex]::Escape($script:Secret))

            $out = Get-TFOutput -Workspace $ws

            # Non-sensitive output carries only the public fields; the reserved
            # 'sensitive' key was stripped.
            $out.result.value.id   | Should -Be 'sensitive-test'
            $out.result.value.name | Should -Be 'svc-1'
            $out.result.value.PSObject.Properties.Name | Should -Not -Contain 'sensitive'
            ($out.result.value | ConvertTo-Json -Compress) | Should -Not -Match ([regex]::Escape($script:Secret))

            # The sensitive output carries the derived token (proving the script
            # saw the merged $InputData.secret) and the provider-level secret
            # (proving $global:ProviderData.SensitiveData was seeded).
            $out.sensitive_result.value.token   | Should -Be "generated-$script:Secret"
            $out.sensitive_result.value.api_key | Should -Be 'provider-api-key-value'

            # Terraform itself marks the output value sensitive.
            $out.sensitive_result.sensitive | Should -BeTrue
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
    }

    It 'refreshes cleanly with sensitive data present (read re-emits the sensitive key)' {
        Write-TFLog 'refresh idempotency with sensitive attributes' 'STEP'
        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir 'sensitive-data') -Variables @{ secret = $script:Secret }
        try {
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null

            # A second plan runs the read script (which re-emits the same object,
            # including the sensitive key) and must be empty.
            $plan = Invoke-TFExit -Workspace $ws -Arguments @('plan', '-detailed-exitcode', '-no-color')
            $plan.ExitCode | Should -Be 0
            ($plan.Output -join "`n") | Should -Not -Match ([regex]::Escape($script:Secret))
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
    }
}
