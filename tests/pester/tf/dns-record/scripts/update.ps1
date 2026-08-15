# update.ps1 - changes the A record's address in place by replacing the record.
$server = $InputData.dns_server
$zone   = $InputData.zone
$name   = $InputData.record_name
$ip     = $InputData.ipv4_address
if ((-not $server -or -not $zone -or -not $name) -and $InputData.id) {
    $parts = ([string]$InputData.id) -split '/', 3
    if ($parts.Count -eq 3) { $server = $parts[0]; $zone = $parts[1]; $name = $parts[2] }
}

Get-DnsServerResourceRecord -ComputerName $server -ZoneName $zone -Name $name -RRType A -ErrorAction SilentlyContinue |
    ForEach-Object { Remove-DnsServerResourceRecord -ComputerName $server -ZoneName $zone -InputObject $_ -Force -ErrorAction SilentlyContinue }

Add-DnsServerResourceRecordA -ComputerName $server -ZoneName $zone -Name $name -IPv4Address $ip -ErrorAction Stop | Out-Null

[PSCustomObject]@{
    id           = "$server/$zone/$name"
    dns_server   = $server
    zone         = $zone
    record_name  = $name
    ipv4_address = $ip
}
