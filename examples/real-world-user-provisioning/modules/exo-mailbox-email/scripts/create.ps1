# create.ps1 - Set the mailbox primary SMTP address in Exchange Online.
# Runs in the EXO sidecar, where the provider's startup_script already ran
# Connect-ExchangeOnline. The UPN arrives via input_data from the AD user module.
$upn   = $InputData.user_principal_name
$email = $InputData.email

# Capital "SMTP:" marks the address primary.
Set-Mailbox -Identity $upn -WindowsEmailAddress $email
Set-Mailbox -Identity $upn -EmailAddresses @{ add = "SMTP:$email" }

$mbx = Get-Mailbox -Identity $upn
[PSCustomObject]@{
  id             = $mbx.ExternalDirectoryObjectId   # stable EXO object id
  user_principal = $mbx.UserPrincipalName
  primary_smtp   = $mbx.PrimarySmtpAddress.ToString()
}
