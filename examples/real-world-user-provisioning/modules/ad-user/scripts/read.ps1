# read.ps1 - Report the current state of the AD user, located by id (ObjectGUID).
$u = Get-ADUser -Identity $InputData.id -Properties Department `
  -Server $global:ProviderState["server"] -Credential $global:ProviderState["credential"] `
  -ErrorAction SilentlyContinue

if ($u) {
  [PSCustomObject]@{
    id                  = $u.ObjectGUID.ToString()
    sam_account_name    = $u.SamAccountName
    user_principal_name = $u.UserPrincipalName
    display_name        = $u.Name
    distinguished_name  = $u.DistinguishedName
  }
}
# Emitting nothing -> Terraform sees the user is gone and plans to recreate it.
