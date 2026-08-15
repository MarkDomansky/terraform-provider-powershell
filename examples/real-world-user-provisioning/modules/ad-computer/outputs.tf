output "id" {
  description = "ObjectGUID of the computer account."
  value       = powershell_script.this.id
}

output "computer_name" {
  value = jsondecode(powershell_script.this.output_data).computer_name
}

output "managed_by" {
  value = jsondecode(powershell_script.this.output_data).managed_by
}

output "distinguished_name" {
  value = jsondecode(powershell_script.this.output_data).distinguished_name
}
