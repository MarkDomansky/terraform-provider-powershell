# read.ps1 - file()-loaded read script. Resolves the file by id (or file_path) and
# reports its current content; emits nothing if the file is gone so Terraform drops
# it from state.
$filePath = $InputData.file_path
if (-not $filePath) { $filePath = $InputData.id }

if (Test-Path $filePath) {
    [PSCustomObject]@{
        id          = $filePath
        file_path   = $filePath
        content     = (Get-Content $filePath -Raw)
        size        = (Get-Item $filePath).Length
        loaded_from = 'file'
    }
}
