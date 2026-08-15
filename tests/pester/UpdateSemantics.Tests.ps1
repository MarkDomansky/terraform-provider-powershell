# End-to-end tests for the update-vs-replace semantics, driven by real terraform
# against the compiled provider:
#   - input changes with an update_script run it in place (no replacement);
#   - script edits are config-only updates that execute nothing;
#   - input changes WITHOUT an update_script force a replacement.
# The tf/update-semantics scripts append to marker logs so each lifecycle
# execution is countable from the outside.

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'UpdateSemantics suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null

    $script:TfDir = Join-Path $PSScriptRoot 'tf'

    function Get-MarkerCount {
        param([string]$Path)
        if (-not (Test-Path $Path)) { return 0 }
        return @(Get-Content -Path $Path).Count
    }
}

Describe 'powershell_script update/replace semantics (compiled provider via terraform)' {

    BeforeEach {
        $script:MarkerDir = Join-Path ([System.IO.Path]::GetTempPath()) ("pstf-updsem-" + [System.IO.Path]::GetRandomFileName())
        New-Item -ItemType Directory -Force -Path $script:MarkerDir | Out-Null
        $script:MarkerDirHcl = $script:MarkerDir -replace '\\', '/'
    }

    AfterEach {
        Remove-Item -Recurse -Force $script:MarkerDir -ErrorAction SilentlyContinue
    }

    It 'runs update_script in place on input change, and treats script edits as config-only' {
        $createLog = Join-Path $script:MarkerDir 'mutable-create.log'
        $updateLog = Join-Path $script:MarkerDir 'mutable-update.log'
        Write-TFLog "mutable resource semantics, markers in $script:MarkerDir" 'STEP'

        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir 'update-semantics') -Variables @{
            marker_dir = $script:MarkerDirHcl
            content    = 'v1'
        }
        try {
            # Initial create: one create execution, no update.
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            Get-MarkerCount $createLog | Should -Be 1
            Get-MarkerCount $updateLog | Should -Be 0

            # Input change: update runs in place — no second create.
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color', '-var', 'content=v2') | Out-Null
            $out = Get-TFOutput -Workspace $ws
            $out.mutable.value.id      | Should -Be 'mutable-1'
            $out.mutable.value.content | Should -Be 'v2'
            Get-MarkerCount $createLog | Should -Be 1
            Get-MarkerCount $updateLog | Should -Be 1

            # Script edit (interpolated note comment): a config-only update.
            # Nothing executes — both marker counts stay put.
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color', '-var', 'content=v2', '-var', 'note=rev2') | Out-Null
            Get-MarkerCount $createLog | Should -Be 1
            Get-MarkerCount $updateLog | Should -Be 1

            # And the config is now reconciled: an identical plan is empty.
            $plan = Invoke-TFExit -Workspace $ws -Arguments @('plan', '-detailed-exitcode', '-no-color', '-var', 'content=v2', '-var', 'note=rev2')
            $plan.ExitCode | Should -Be 0
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
    }

    It 'forces replacement on input change when update_script is omitted' {
        $createLog = Join-Path $script:MarkerDir 'immutable-create.log'
        Write-TFLog "immutable resource semantics, markers in $script:MarkerDir" 'STEP'

        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir 'update-semantics') -Variables @{
            marker_dir = $script:MarkerDirHcl
            content    = 'v1'
        }
        try {
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            Get-MarkerCount $createLog | Should -Be 1

            # The plan must announce a replacement, not an in-place update.
            $plan = Invoke-TFExit -Workspace $ws -Arguments @('plan', '-detailed-exitcode', '-no-color', '-var', 'content=v2')
            $plan.ExitCode | Should -Be 2
            ($plan.Output -join "`n") | Should -Match 'must be replaced|forces replacement'

            # Applying re-runs the create script (delete + create).
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color', '-var', 'content=v2') | Out-Null
            $out = Get-TFOutput -Workspace $ws
            $out.immutable.value.content | Should -Be 'v2'
            Get-MarkerCount $createLog | Should -Be 2
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
    }

    It 'rejects malformed input_data at plan time' {
        Write-TFLog 'plan-time JSON validation of input_data' 'STEP'
        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir 'invalid-input')
        try {
            $res = Invoke-TFExit -Workspace $ws -Arguments @('plan', '-no-color')
            $res.ExitCode | Should -Not -Be 0
            ($res.Output -join "`n") | Should -Match 'Invalid JSON Object'
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
    }
}
