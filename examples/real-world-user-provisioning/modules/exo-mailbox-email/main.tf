terraform {
  required_providers {
    powershell = {
      source = "registry.terraform.io/markdomansky/powershell"

      # This module runs against an Exchange Online configuration. The caller must
      # fill this slot with a powershell provider whose startup_script ran
      # Connect-ExchangeOnline.
      configuration_aliases = [powershell.exo]
    }
  }
}

resource "powershell_script" "this" {
  provider = powershell.exo

  create_script = file("${path.module}/scripts/create.ps1")
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")

  input_data = jsonencode({
    user_principal_name = var.user_principal_name
    email               = var.email
  })
}
