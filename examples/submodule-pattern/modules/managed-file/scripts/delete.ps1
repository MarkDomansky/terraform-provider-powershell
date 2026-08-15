# delete.ps1 - Deletes a managed file
$filePath = $InputData.file_path
if (-not $filePath) {
    $filePath = $InputData.id
}

if (Test-Path $filePath) {
    Remove-Item -Path $filePath -Force
}
