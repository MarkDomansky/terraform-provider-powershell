# create.ps1 - writes the managed file and uses its path as the id so the object is
# fully recoverable from the id alone (see read.ps1).
$filePath = $InputData.file_path
$content  = $InputData.content

$parentDir = Split-Path -Parent $filePath
if ($parentDir -and -not (Test-Path $parentDir)) {
    New-Item -Path $parentDir -ItemType Directory -Force | Out-Null
}

Set-Content -Path $filePath -Value $content -NoNewline

[PSCustomObject]@{
    id        = $filePath
    file_path = $filePath
    content   = $content
}
