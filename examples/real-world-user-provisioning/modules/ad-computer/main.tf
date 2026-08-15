terraform {
  required_providers {
    powershell = {
      source = "registry.terraform.io/markdomansky/powershell"
    }
  }
}

# The computer is assigned to a user via ManagedBy. owner_dn / owner_display_name
# are passed in by the caller from the ad-user module's output, which is what
# orders this resource after the user.
resource "powershell_script" "this" {
  create_script = file("${path.module}/scripts/create.ps1")
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")

  input_data = jsonencode({
    computer_name      = var.computer_name
    owner_dn           = var.owner_dn
    owner_display_name = var.owner_display_name
  })
}
