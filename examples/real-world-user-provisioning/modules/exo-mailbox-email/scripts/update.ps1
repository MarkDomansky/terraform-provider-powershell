# update.ps1 - Re-apply the desired primary SMTP address (id identifies the mailbox).
Set-Mailbox -Identity $InputData.id -WindowsEmailAddress $InputData.email

$mbx = Get-Mailbox -Identity $InputData.id
[PSCustomObject]@{
  id             = $mbx.ExternalDirectoryObjectId
  user_principal = $mbx.UserPrincipalName
  primary_smtp   = $mbx.PrimarySmtpAddress.ToString()
}
