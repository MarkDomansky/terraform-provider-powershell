# delete.ps1 - Remove the computer account identified by id.
Remove-ADComputer -Identity $InputData.id -Confirm:$false `
  -Server $global:ProviderState["server"] -Credential $global:ProviderState["credential"]
