# Provider startup script, loaded from a .ps1 file with file(). Seeds the
# provider-level global that the resource create script appends to, and drops a
# marker so the test can confirm it ran. The marker directory arrives via
# $global:ProviderData.Data (the provider's provider_data argument) because
# file()-loaded scripts are read verbatim by terraform and never interpolated.
$global:Provisioned = [System.Collections.Generic.List[string]]::new()
$dir = $global:ProviderData.Data.marker_dir
"started" | Out-File -FilePath "$dir/startup.txt" -Encoding utf8
