# create.ps1 - Creates a managed file
$filePath = $InputData.file_path
$content  = $InputData.content

# Ensure parent directory exists
$parentDir = Split-Path -Parent $filePath
if ($parentDir -and -not (Test-Path $parentDir)) {
    New-Item -Path $parentDir -ItemType Directory -Force | Out-Null
}

# Create the file
Set-Content -Path $filePath -Value $content -NoNewline

[PSCustomObject]@{
    id        = $filePath
    file_path = $filePath
    content   = $content
    size      = (Get-Item $filePath).Length
    created   = (Get-Date).ToString("o")
}
