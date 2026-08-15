# delete.ps1 - Remove the AD user identified by id.
Remove-ADUser -Identity $InputData.id -Confirm:$false `
  -Server $global:ProviderState["server"] -Credential $global:ProviderState["credential"]
