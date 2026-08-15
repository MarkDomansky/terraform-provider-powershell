# create.ps1 - Create an AD computer account assigned to a user.
$name  = $InputData.computer_name
$owner = $InputData.owner_dn                              # the user's DN
$desc  = "Workstation for $($InputData.owner_display_name)"

$c = New-ADComputer `
  -Server      $global:ProviderState["server"] `
  -Credential  $global:ProviderState["credential"] `
  -Path        $global:ProviderState["base_ou"] `
  -Name        $name `
  -ManagedBy   $owner `
  -Description  $desc `
  -Enabled     $true `
  -PassThru

[PSCustomObject]@{
  id                 = $c.ObjectGUID.ToString()
  computer_name      = $c.Name
  managed_by         = $owner
  distinguished_name = $c.DistinguishedName
}
