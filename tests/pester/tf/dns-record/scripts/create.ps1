# create.ps1 - creates a DNS A record on the target server. Runs ON the DNS server
# via the provider's WinRM session. The id is "<server>/<zone>/<name>" so the
# object is fully recoverable from the id alone (see read.ps1).
$server = $InputData.dns_server
$zone   = $InputData.zone
$name   = $InputData.record_name
$ip     = $InputData.ipv4_address

# Idempotent: clear any stale A record of the same name before adding.
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
