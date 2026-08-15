# Provider-alias tests, driven by real terraform against the compiled provider.
# These prove the core aliasing guarantees end to end:
#   * each `provider "powershell"` configuration (default + alias) runs in its
#     own sidecar process (distinct $PID -> distinct runspace), and
#   * one alias cannot read another alias's globals (here $global:Shared).
# Because terraform itself routes `provider = powershell.<alias>`, this also
# verifies the wiring a user actually writes in HCL.
#
# The terraform config lives in real .tf files under tf/alias/ so it can be
# parsed and validated by terraform tooling; the test just points the workspace
# at that folder.
#
# This is the Pester counterpart to the (now removed) Go alias unit test; doing
# it through terraform additionally exercises terraform's per-configuration
# provider instancing, which the Go test could not.

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'Alias suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null

    $script:AliasConfigPath = Join-Path $PSScriptRoot 'tf/alias'
}

Describe 'provider aliases (compiled provider via terraform)' {

    BeforeAll { #runs once before all It blocks in this Describe
        Write-TFLog 'applying alias config (default + beta)' 'STEP'
        $script:ws = New-TFWorkspace -ConfigPath $script:AliasConfigPath
        Invoke-TF -Workspace $script:ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
        $script:out = Get-TFOutput -Workspace $script:ws
    }

    AfterAll { #runs once after all It blocks in this Describe
        if ($script:ws) { Remove-TFWorkspace -Workspace $script:ws }
    }

    It 'runs each alias in its own sidecar process (distinct PIDs)' {
        $script:out.a.value.pid | Should -Not -BeNullOrEmpty
        $script:out.b.value.pid | Should -Not -BeNullOrEmpty
        $script:out.a.value.pid | Should -Not -Be $script:out.b.value.pid
    }

    It 'gives each alias its own provider state' {
        $script:out.a.value.secret | Should -Be 'alpha-secret'
        $script:out.b.value.secret | Should -Be 'beta-secret'
    }

    It 'does not let one alias read another alias''s private state' {
        # 'owner' is set only by the default configuration's startup script.
        $script:out.a.value.owner | Should -Be 'default'
        $script:out.b.value.owner | Should -BeNullOrEmpty
    }
}
