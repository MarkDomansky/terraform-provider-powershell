# update.ps1 - Re-point ManagedBy if the owner changed (id identifies the object).
Set-ADComputer -Identity $InputData.id -ManagedBy $InputData.owner_dn `
  -Server $global:ProviderState["server"] -Credential $global:ProviderState["credential"]

$c = Get-ADComputer -Identity $InputData.id -Properties ManagedBy `
  -Server $global:ProviderState["server"] -Credential $global:ProviderState["credential"]

[PSCustomObject]@{
  id                 = $c.ObjectGUID.ToString()
  computer_name      = $c.Name
  managed_by         = $c.ManagedBy
  distinguished_name = $c.DistinguishedName
}
