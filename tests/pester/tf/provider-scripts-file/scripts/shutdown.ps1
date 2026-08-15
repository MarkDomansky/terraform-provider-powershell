# Provider shutdown script, loaded from a .ps1 file with file(). Runs once when the
# provider tears down, in the same persistent runspace, so it can read the global
# the startup script created and the create script appended to. Writes the
# accumulated list to a marker file for the test to assert on.
$dir = $global:ProviderData.Data.marker_dir
($global:Provisioned -join ',') | Out-File -FilePath "$dir/shutdown.txt" -Encoding utf8
