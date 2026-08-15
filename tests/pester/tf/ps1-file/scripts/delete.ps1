# delete.ps1 - file()-loaded delete script. Removes the managed file by id.
$filePath = $InputData.file_path
if (-not $filePath) { $filePath = $InputData.id }

if (Test-Path $filePath) {
    Remove-Item -Path $filePath -Force
}
