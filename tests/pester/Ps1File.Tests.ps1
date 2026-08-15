# File-backed script tests, driven by real terraform against the compiled provider.
# These prove the everyday authoring pattern works end to end: CRUD scripts loaded
# from real .ps1 files with file(), with all runtime values passed through
# input_data (because Terraform reads .ps1 files verbatim and does not interpolate
# them). create -> read -> update -> destroy is exercised against the shipped stack.
#
# The terraform config and the .ps1 files live under tf/ps1-file/ so they can be
# parsed/validated by terraform tooling and the scripts can be linted as plain
# PowerShell. The Pester suite copies the whole folder (scripts included) into each
# temp workspace.

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'Ps1File suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null

    $script:ConfigPath = Join-Path $PSScriptRoot 'tf/ps1-file'
}

Describe 'powershell_script with file()-loaded .ps1 scripts (compiled provider via terraform)' {

    It 'creates and reads back a resource whose scripts come from .ps1 files' {
        $filePath = (Join-Path ([System.IO.Path]::GetTempPath()) ("pstf-ps1file-" + [System.IO.Path]::GetRandomFileName() + ".txt")) -replace '\\', '/'
        Write-TFLog "create/read scenario, file=$filePath" 'STEP'

        $ws = New-TFWorkspace -ConfigPath $script:ConfigPath -Variables @{
            file_path = $filePath
            content   = 'hello-from-ps1-file'
        }
        try {
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $out = Get-TFOutput -Workspace $ws

            Write-TFLog "asserting created state for id=$($out.result.value.id)"
            $out.result.value.id          | Should -Be $filePath
            $out.result.value.content     | Should -Be 'hello-from-ps1-file'
            $out.result.value.loaded_from | Should -Be 'file'
            Test-Path $filePath           | Should -BeTrue
            (Get-Content $filePath -Raw)  | Should -Be 'hello-from-ps1-file'
        } finally {
            Remove-TFWorkspace -Workspace $ws
        }
        Write-TFLog 'create/read scenario torn down; file should be gone'
        Test-Path $filePath | Should -BeFalse
    }

    It 'runs the file()-loaded update script in place' {
        $filePath = (Join-Path ([System.IO.Path]::GetTempPath()) ("pstf-ps1file-" + [System.IO.Path]::GetRandomFileName() + ".txt")) -replace '\\', '/'
        Write-TFLog "update scenario, file=$filePath" 'STEP'

        $ws = New-TFWorkspace -ConfigPath $script:ConfigPath -Variables @{
            file_path = $filePath
            content   = 'v1'
        }
        try {
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            (Get-Content $filePath -Raw) | Should -Be 'v1'

            # Re-apply in the SAME workspace with new content. content is not a
            # force-replace attribute, so this drives the update_script rather than a
            # destroy/create. Rewriting terraform.tfvars keeps the existing state.
            Write-TFLog 'rewriting tfvars to v2 to trigger update_script'
            Set-Content -Path (Join-Path $ws.Dir 'terraform.tfvars') -Encoding UTF8 -Value @(
                "file_path = `"$filePath`""
                'content = "v2"'
            )
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $out = Get-TFOutput -Workspace $ws
            $out.result.value.content | Should -Be 'v2'
            (Get-Content $filePath -Raw) | Should -Be 'v2'
        } finally {
            Remove-TFWorkspace -Workspace $ws
            Remove-Item -Path $filePath -Force -ErrorAction SilentlyContinue
        }
    }
}
