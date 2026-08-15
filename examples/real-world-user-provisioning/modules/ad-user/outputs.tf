output "id" {
  description = "ObjectGUID of the AD user."
  value       = powershell_script.this.id
}

output "sam_account_name" {
  value = jsondecode(powershell_script.this.output_data).sam_account_name
}

output "user_principal_name" {
  value = jsondecode(powershell_script.this.output_data).user_principal_name
}

output "display_name" {
  value = jsondecode(powershell_script.this.output_data).display_name
}

output "distinguished_name" {
  value = jsondecode(powershell_script.this.output_data).distinguished_name
}
