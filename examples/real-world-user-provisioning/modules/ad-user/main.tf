terraform {
  required_providers {
    powershell = {
      source = "registry.terraform.io/markdomansky/powershell"
    }
  }
}

# Active Directory connection details (server, credential, base OU) come from
# $global:ProviderState, which the root provider's startup_script creates and
# populates (the persistent runspace keeps it alive for every resource). Only the
# per-user values travel through input_data.
resource "powershell_script" "this" {
  create_script = file("${path.module}/scripts/create.ps1")
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")

  input_data = jsonencode({
    first_name = var.first_name
    last_name  = var.last_name
    department = var.department
    domain     = var.domain
  })
}
