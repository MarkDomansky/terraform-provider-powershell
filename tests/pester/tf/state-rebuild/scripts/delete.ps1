# delete.ps1 - removes the managed file by id.
$filePath = $InputData.file_path
if (-not $filePath) { $filePath = $InputData.id }

if ($filePath -and (Test-Path $filePath)) {
    Remove-Item -Path $filePath -Force
}
