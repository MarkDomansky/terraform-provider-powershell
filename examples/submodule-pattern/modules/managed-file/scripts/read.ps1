# read.ps1 - Reads current state of a managed file
$filePath = $InputData.file_path
if (-not $filePath) {
    $filePath = $InputData.id
}

if (Test-Path $filePath) {
    $item = Get-Item $filePath
    [PSCustomObject]@{
        id        = $filePath
        file_path = $filePath
        content   = (Get-Content $filePath -Raw)
        size      = $item.Length
        modified  = $item.LastWriteTime.ToString("o")
    }
}
# If file doesn't exist, nothing is emitted -> Terraform removes from state
