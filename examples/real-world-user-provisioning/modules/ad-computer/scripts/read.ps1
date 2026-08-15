# read.ps1 - Report the current state of the computer account, located by id.
$c = Get-ADComputer -Identity $InputData.id -Properties ManagedBy `
  -Server $global:ProviderState["server"] -Credential $global:ProviderState["credential"] `
  -ErrorAction SilentlyContinue

if ($c) {
  [PSCustomObject]@{
    id                 = $c.ObjectGUID.ToString()
    computer_name      = $c.Name
    managed_by         = $c.ManagedBy
    distinguished_name = $c.DistinguishedName
  }
}
# Emitting nothing -> Terraform sees the computer is gone and plans to recreate it.
