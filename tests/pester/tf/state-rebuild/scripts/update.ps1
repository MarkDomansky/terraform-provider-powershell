# update.ps1 - rewrites the managed file in place to the desired content.
$filePath = $InputData.file_path
if (-not $filePath) { $filePath = $InputData.id }
$content = $InputData.content

Set-Content -Path $filePath -Value $content -NoNewline

[PSCustomObject]@{
    id        = $filePath
    file_path = $filePath
    content   = $content
}
