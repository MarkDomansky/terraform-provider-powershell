output "id" {
  description = "ExternalDirectoryObjectId of the mailbox."
  value       = powershell_script.this.id
}

output "primary_smtp" {
  value = jsondecode(powershell_script.this.output_data).primary_smtp
}
