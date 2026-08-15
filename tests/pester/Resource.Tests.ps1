# End-to-end tests for the powershell_script resource, driven by real terraform
# against the compiled provider. This is the Pester counterpart to the Go
# acceptance tests in tests/acceptance_test.go.
#
# The terraform configs live in real .tf files under tf/ (one folder per
# scenario) so they can be parsed and validated by terraform tooling. Runtime
# values, such as the temp file path, are passed in as terraform variables via
# the -Variables parameter (written to terraform.tfvars).

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'Resource suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null

    $script:TfDir = Join-Path $PSScriptRoot 'tf'
}

Describe 'powershell_script resource (compiled provider via terraform)' {

    It 'creates, reads back, and destroys a file-backed resource' {
        # A forward-slashed temp path keeps the HCL valid on Windows.
        $filePath = (Join-Path ([System.IO.Path]::GetTempPath()) ("pstf-basic-" + [System.IO.Path]::GetRandomFileName() + ".txt")) -replace '\\', '/'
        Write-TFLog "basic create/read/destroy, file=$filePath" 'STEP'

        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir 'resource-basic') -Variables @{ file_path = $filePath }
        try {
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $out = Get-TFOutput -Workspace $ws

            $out.result.value.id | Should -Be 'test-file'
            Test-Path $filePath | Should -BeTrue

            # Destroy must run the delete script and remove the file.
            Invoke-TF -Workspace $ws -Arguments @('destroy', '-auto-approve', '-no-color') | Out-Null
            Test-Path $filePath | Should -BeFalse
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
    }

    It 'passes JSON input to scripts and returns JSON output' {
        Write-TFLog 'JSON input/output round-trip' 'STEP'
        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir 'resource-json')
        try {
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $out = Get-TFOutput -Workspace $ws

            $out.result.value.id       | Should -Be 'json-test'
            $out.result.value.greeting | Should -Be 'Hello, Terraform!'
            # count is doubled (21 * 2); JSON numbers come back as int.
            [int]$out.result.value.count | Should -Be 42
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
    }

    It 'runs the provider startup script and shares its state with resources' {
        $filePath = (Join-Path ([System.IO.Path]::GetTempPath()) ("pstf-startup-" + [System.IO.Path]::GetRandomFileName() + ".txt")) -replace '\\', '/'
        Write-TFLog "startup-state sharing, file=$filePath" 'STEP'

        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir 'resource-startup') -Variables @{ file_path = $filePath }
        try {
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $out = Get-TFOutput -Workspace $ws

            $out.result.value.id   | Should -Be 'startup-test'
            # The resource picked up base_path from provider startup state.
            $out.result.value.path | Should -Be $filePath
            Test-Path $filePath    | Should -BeTrue
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
    }

    # Both provider-level scripts (startup_script + shutdown_script) are exercised
    # for BOTH authoring styles: inline heredocs (tf/provider-scripts) and file()-
    # loaded .ps1 files (tf/provider-scripts-file). The Pester side is identical for
    # each because both configs take the marker dir as a variable and honour the same
    # startup -> create -> shutdown state-sharing contract.
    It 'runs the provider startup and shutdown scripts (<Source>), sharing state across the whole run' -ForEach @(
        @{ Source = 'inline'; Config = 'provider-scripts' }
        @{ Source = 'file';   Config = 'provider-scripts-file' }
    ) {
        # A fresh empty dir per run so the startup/shutdown markers are unambiguous.
        $markerDir = Join-Path ([System.IO.Path]::GetTempPath()) ("pstf-provscripts-" + [System.IO.Path]::GetRandomFileName())
        New-Item -ItemType Directory -Force -Path $markerDir | Out-Null
        $markerDirHcl = $markerDir -replace '\\', '/'
        $startupMarker  = Join-Path $markerDir 'startup.txt'
        $shutdownMarker = Join-Path $markerDir 'shutdown.txt'
        Write-TFLog "provider startup+shutdown scripts ($Source), markers in $markerDir" 'STEP'

        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir $Config) -Variables @{ marker_dir = $markerDirHcl }
        try {
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $out = Get-TFOutput -Workspace $ws

            $out.result.value.id | Should -Be 'provider-scripts-test'
            # The resource saw the global the startup script created (list had 1 entry after Add).
            [int]$out.result.value.provisioned | Should -Be 1

            # The startup script ran and left its marker.
            Test-Path $startupMarker | Should -BeTrue

            # The shutdown script runs when the provider tears down at the end of the
            # terraform command. terraform waits for the plugin process to exit, but
            # the shutdown script writes its marker in that teardown window, so poll
            # briefly to avoid a race with the file appearing on disk.
            $deadline = (Get-Date).AddSeconds(15)
            while (-not (Test-Path $shutdownMarker) -and (Get-Date) -lt $deadline) {
                Start-Sleep -Milliseconds 200
            }
            Test-Path $shutdownMarker | Should -BeTrue

            # The shutdown script read the SAME global the resource appended to,
            # proving startup, create, and shutdown all shared one persistent runspace.
            (Get-Content -Path $shutdownMarker -Raw).Trim() | Should -Be 'res-1'
        } finally {
            Remove-TFWorkspace -Workspace $ws
            Remove-Item -Recurse -Force $markerDir -ErrorAction SilentlyContinue
        }
    }

    It 'manages multiple resources through one provider' {
        Write-TFLog 'multiple resources share one sidecar' 'STEP'
        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir 'resource-multi')
        try {
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $out = Get-TFOutput -Workspace $ws

            $out.a.value.id | Should -Be 'res-a'
            $out.b.value.id | Should -Be 'res-b'
            # Both resources share one provider configuration, hence one sidecar PID.
            $out.a.value.pid | Should -Be $out.b.value.pid
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
    }

    It 'enforces the provider timeout by killing a script that runs too long' {
        Write-TFLog 'provider timeout enforcement (script sleeps past timeout)' 'STEP'
        $ws = New-TFWorkspace -ConfigPath (Join-Path $script:TfDir 'provider-timeout')
        try {
            # The create script sleeps 60s against an 8s provider timeout, so apply must
            # fail. Use Invoke-TFExit (not Invoke-TF) so the non-zero exit is data, not a
            # thrown terminating error, and assert the failure is genuinely a timeout.
            $start = Get-Date
            $res = Invoke-TFExit -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color')
            $elapsed = ((Get-Date) - $start).TotalSeconds

            $res.ExitCode | Should -Not -Be 0
            ($res.Output -join "`n") | Should -Match 'timed out'
            # The sidecar was force-killed at the timeout, so apply returned well before
            # the script's full 60s sleep would have elapsed.
            $elapsed | Should -BeLessThan 45
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
    }
}
