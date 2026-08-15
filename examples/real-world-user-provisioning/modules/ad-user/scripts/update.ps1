# update.ps1 - Apply in-place changes (id carries the existing object identity).
Set-ADUser -Identity $InputData.id -Department $InputData.department `
  -Server $global:ProviderState["server"] -Credential $global:ProviderState["credential"]

$u = Get-ADUser -Identity $InputData.id -Properties Department `
  -Server $global:ProviderState["server"] -Credential $global:ProviderState["credential"]

# Same shape as create/read so a refresh never drops a key.
[PSCustomObject]@{
  id                  = $u.ObjectGUID.ToString()
  sam_account_name    = $u.SamAccountName
  user_principal_name = $u.UserPrincipalName
  display_name        = $u.Name
  distinguished_name  = $u.DistinguishedName
}
