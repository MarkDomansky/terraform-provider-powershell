# State-rebuild test, driven by real terraform against the compiled provider.
# Proves that a lost terraform state can be rebuilt purely from the information in
# the TF file: after the state file is deleted, `terraform import` (which carries
# only the resource id - and here the id IS the file path from the TF file) brings
# the resource back under management under its original id, and a reconciling apply
# converges to a clean plan with the managed object matching config. This exercises
# the provider's ImportState passthrough and the import refresh path end to end.
#
# (create_script is a force-replace attribute that import cannot restore from the
# id alone, so the reconciling apply re-materialises the object from config rather
# than adopting it byte-for-byte; the rebuilt state is what we assert on.)
#
# It runs everywhere (the managed object is a local file, so it is cross-platform
# and needs no remote host), unlike the DNS/remote integration suites.
#
# The terraform config and .ps1 scripts live under tf/state-rebuild/.

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'StateRebuild suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null

    $script:ConfigPath = Join-Path $PSScriptRoot 'tf/state-rebuild'
}

Describe 'rebuilding lost state from the TF file via import (compiled provider via terraform)' {

    It 'reimports the same object by id after the state file is deleted' {
        $filePath = (Join-Path ([System.IO.Path]::GetTempPath()) ("pstf-rebuild-" + [System.IO.Path]::GetRandomFileName() + ".txt")) -replace '\\', '/'
        Write-TFLog "state-rebuild scenario, file=$filePath" 'STEP'

        $ws = New-TFWorkspace -ConfigPath $script:ConfigPath -Variables @{
            file_path = $filePath
            content   = 'rebuild-me'
        }
        try {
            # 1. Create the resource and capture the durable id from the TF file.
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $out = Get-TFOutput -Workspace $ws
            $id = $out.result.value.id
            $id | Should -Be $filePath
            Test-Path $filePath | Should -BeTrue
            Write-TFLog "created id=$id; simulating state loss"

            # 2. Simulate catastrophic state loss: delete the state file(s) while the
            #    real object lives on. The TF file (config + id) is all we keep.
            #    (terraform errors rather than reporting "empty" with no state file, so
            #    we assert the loss at the filesystem level.)
            Get-ChildItem -Path $ws.Dir -Filter 'terraform.tfstate*' | Remove-Item -Force
            Test-Path (Join-Path $ws.Dir 'terraform.tfstate') | Should -BeFalse
            Write-TFLog 'state file deleted; rebuilding via terraform import'

            # 3. Rebuild state from the id alone. `terraform import` carries only the
            #    id; that id IS the information in the TF file (it is the file path),
            #    so it is enough to bring the resource back under management.
            Invoke-TF -Workspace $ws -Arguments @('import', '-no-color', 'powershell_script.this', $id) | Out-Null
            $stateAfter = Invoke-TF -Workspace $ws -Arguments @('state', 'list', '-no-color')
            ($stateAfter | Out-String) | Should -Match 'powershell_script\.this'
            Write-TFLog 'resource is back in state under its original id after import'

            # 4. Confirm the rebuilt state is addressed by the original id (not a new,
            #    unreconstructable one) - i.e. the durable handle from the TF file was
            #    preserved through the rebuild.
            $imported = Invoke-TF -Workspace $ws -Arguments @('state', 'show', '-no-color', 'powershell_script.this')
            ($imported | Out-String) | Should -Match ([regex]::Escape($id))

            # 5. Reconcile the config (scripts/input_data, which import can't restore)
            #    back into state, then prove the rebuild is complete: a follow-up plan
            #    reports no changes at all and the managed object matches the TF file.
            Invoke-TF -Workspace $ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
            $clean = Invoke-TFExit -Workspace $ws -Arguments @('plan', '-no-color', '-detailed-exitcode')
            Write-TFLog "post-rebuild plan detailed-exitcode = $($clean.ExitCode)"
            $clean.ExitCode | Should -Be 0

            $rebuilt = Get-TFOutput -Workspace $ws
            $rebuilt.result.value.id      | Should -Be $id
            $rebuilt.result.value.content | Should -Be 'rebuild-me'
            (Get-Content $filePath -Raw)  | Should -Be 'rebuild-me'
        } finally {
            Remove-TFWorkspace -Workspace $ws
            Remove-Item -Path $filePath -Force -ErrorAction SilentlyContinue
        }
        Write-TFLog 'state-rebuild scenario torn down; object should be gone'
        Test-Path $filePath | Should -BeFalse
    }
}
