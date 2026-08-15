# read.ps1 - rebuilds state from the id alone. `terraform import` passes only the
# id (the file path), so resolving by id is what makes a lost state recoverable
# from the TF file. Emits nothing if the object is gone.
$filePath = $InputData.id
if (-not $filePath) { $filePath = $InputData.file_path }

if ($filePath -and (Test-Path $filePath)) {
    [PSCustomObject]@{
        id        = $filePath
        file_path = $filePath
        content   = (Get-Content $filePath -Raw)
    }
}
