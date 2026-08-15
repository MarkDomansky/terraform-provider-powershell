# delete.ps1 - removes the A record. Resolves the target from input_data or the
# self-describing id, then deletes any matching A records.
$server = $InputData.dns_server
$zone   = $InputData.zone
$name   = $InputData.record_name
if ((-not $server -or -not $zone -or -not $name) -and $InputData.id) {
    $parts = ([string]$InputData.id) -split '/', 3
    if ($parts.Count -eq 3) { $server = $parts[0]; $zone = $parts[1]; $name = $parts[2] }
}

Get-DnsServerResourceRecord -ComputerName $server -ZoneName $zone -Name $name -RRType A -ErrorAction SilentlyContinue |
    ForEach-Object { Remove-DnsServerResourceRecord -ComputerName $server -ZoneName $zone -InputObject $_ -Force -ErrorAction SilentlyContinue }
