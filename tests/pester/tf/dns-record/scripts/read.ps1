# read.ps1 - reports the current A record. Resolves the record from input_data, or,
# when only an id is present (as after `terraform import`), by splitting the
# self-describing "<server>/<zone>/<name>" id. Emits nothing if the record is gone
# so Terraform drops it from state.
$server = $InputData.dns_server
$zone   = $InputData.zone
$name   = $InputData.record_name
if ((-not $server -or -not $zone -or -not $name) -and $InputData.id) {
    $parts = ([string]$InputData.id) -split '/', 3
    if ($parts.Count -eq 3) { $server = $parts[0]; $zone = $parts[1]; $name = $parts[2] }
}

$rec = Get-DnsServerResourceRecord -ComputerName $server -ZoneName $zone -Name $name -RRType A -ErrorAction SilentlyContinue |
    Select-Object -First 1
if ($rec) {
    [PSCustomObject]@{
        id           = "$server/$zone/$name"
        dns_server   = $server
        zone         = $zone
        record_name  = $name
        ipv4_address = $rec.RecordData.IPv4Address.IPAddressToString
    }
}
