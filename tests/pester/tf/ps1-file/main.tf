terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# Proves the provider works when the CRUD scripts are loaded from real .ps1 files
# with file(), rather than inline heredocs. Terraform reads the .ps1 files verbatim
# (no interpolation), so every runtime value reaches PowerShell through input_data
# and is read back from the $InputData parameter. The Pester test supplies unique
# values via terraform.tfvars.
variable "file_path" {
  type = string
}

variable "content" {
  type = string
}

provider "powershell" {}

resource "powershell_script" "this" {
  create_script = file("${path.module}/scripts/create.ps1")
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")

  input_data = jsonencode({
    file_path = var.file_path
    content   = var.content
  })

  triggers = {
    content_hash = sha256(var.content)
  }
}

output "result" { value = jsondecode(powershell_script.this.output_data) }
