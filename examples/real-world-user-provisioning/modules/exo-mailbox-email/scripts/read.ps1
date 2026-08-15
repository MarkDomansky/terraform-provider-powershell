# read.ps1 - Report the mailbox's current primary SMTP address, located by id.
$mbx = Get-Mailbox -Identity $InputData.id -ErrorAction SilentlyContinue

if ($mbx) {
  [PSCustomObject]@{
    id             = $mbx.ExternalDirectoryObjectId
    user_principal = $mbx.UserPrincipalName
    primary_smtp   = $mbx.PrimarySmtpAddress.ToString()
  }
}
# Emitting nothing -> the mailbox is gone; Terraform plans to re-apply the address.
