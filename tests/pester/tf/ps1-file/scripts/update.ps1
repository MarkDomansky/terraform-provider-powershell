# update.ps1 - file()-loaded update script. $InputData carries the desired new
# content plus the existing id; rewrite the file in place and report the new state.
$filePath = $InputData.file_path
if (-not $filePath) { $filePath = $InputData.id }
$content = $InputData.content

Set-Content -Path $filePath -Value $content -NoNewline

[PSCustomObject]@{
    id          = $filePath
    file_path   = $filePath
    content     = $content
    size        = (Get-Item $filePath).Length
    loaded_from = 'file'
}
