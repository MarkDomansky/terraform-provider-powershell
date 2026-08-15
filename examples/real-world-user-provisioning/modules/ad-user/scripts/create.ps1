# create.ps1 - Create an Active Directory user.
$first = $InputData.first_name
$last  = $InputData.last_name
$sam   = ("$first.$last").ToLower()
$upn   = "$sam@$($InputData.domain)"
$name  = "$first $last"

New-ADUser `
  -Server            $global:ProviderState["server"] `
  -Credential        $global:ProviderState["credential"] `
  -Path              $global:ProviderState["base_ou"] `
  -Name              $name `
  -GivenName         $first `
  -Surname           $last `
  -SamAccountName    $sam `
  -UserPrincipalName $upn `
  -Department        $InputData.department `
  -Enabled           $false   # disabled until SSPR / onboarding sets a password

# Password set and reset are deliberately outside this Terraform run; the account
# is activated out-of-band by the existing self-service password reset process.

$u = Get-ADUser -Identity $sam `
  -Server $global:ProviderState["server"] -Credential $global:ProviderState["credential"]

[PSCustomObject]@{
  id                  = $u.ObjectGUID.ToString()   # durable handle for R/U/D
  sam_account_name    = $u.SamAccountName
  user_principal_name = $u.UserPrincipalName
  display_name        = $u.Name
  distinguished_name  = $u.DistinguishedName
}
