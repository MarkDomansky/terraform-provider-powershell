output "resource_id" {
  description = "The ID of the managed file resource."
  value       = powershell_script.this.id
}

output "output_data" {
  description = "Full output data from the PowerShell scripts."
  value       = jsondecode(powershell_script.this.output_data)
}
