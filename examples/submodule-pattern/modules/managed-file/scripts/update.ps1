# update.ps1 - Updates a managed file's content
$filePath = $InputData.file_path
if (-not $filePath) {
    $filePath = $InputData.id
}
$content = $InputData.content

Set-Content -Path $filePath -Value $content -NoNewline

[PSCustomObject]@{
    id        = $filePath
    file_path = $filePath
    content   = $content
    size      = (Get-Item $filePath).Length
    updated   = (Get-Date).ToString("o")
}
