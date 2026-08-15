# create.ps1 - file()-loaded create script. Reads every value from $InputData
# (Terraform does NOT interpolate .ps1 files), writes the managed file, and emits
# exactly one object whose id is the file path so read/update/delete can find it.
$filePath = $InputData.file_path
$content  = $InputData.content

$parentDir = Split-Path -Parent $filePath
if ($parentDir -and -not (Test-Path $parentDir)) {
    New-Item -Path $parentDir -ItemType Directory -Force | Out-Null
}

Set-Content -Path $filePath -Value $content -NoNewline

[PSCustomObject]@{
    id           = $filePath
    file_path    = $filePath
    content      = $content
    size         = (Get-Item $filePath).Length
    loaded_from  = 'file'
}
