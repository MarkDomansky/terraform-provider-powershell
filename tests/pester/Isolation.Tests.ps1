# Resource-script isolation tests, driven by real terraform against the compiled
# provider. These prove the end-to-end counterpart of the Go/C# unit tests: a bare
# variable created by one resource's script does not leak into a later resource's
# script, because CRUD scripts run in an isolated child scope of the shared runspace.
#
# The terraform config lives in real .tf files under tf/resource-isolation/ so it
# can be parsed and validated by terraform tooling.

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'Isolation suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null

    $script:IsolationConfigPath = Join-Path $PSScriptRoot 'tf/resource-isolation'
}

Describe 'resource script isolation (compiled provider via terraform)' {

    BeforeAll {
        Write-TFLog 'applying isolation config (two resources, leak probe)' 'STEP'
        $script:ws = New-TFWorkspace -ConfigPath $script:IsolationConfigPath
        Invoke-TF -Workspace $script:ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
        $script:out = Get-TFOutput -Workspace $script:ws
    }

    AfterAll {
        if ($script:ws) { Remove-TFWorkspace -Workspace $script:ws }
    }

    It 'does not leak an incidental variable from one resource into the next' {
        # If isolation regressed, this would be 'should-not-escape'.
        $script:out.second_seen.value | Should -BeNullOrEmpty
    }
}
