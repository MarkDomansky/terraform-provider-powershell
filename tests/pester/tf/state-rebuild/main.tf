terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# State-rebuild scenario. The managed object is a file whose path IS the resource
# id, so the id alone (carried by `terraform import`) is enough for the read script
# to reconstruct state from the real object. This is what lets a lost terraform
# state be rebuilt purely from the information in the TF file (the id/config) plus
# the live object, with no separate state backup.
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
}

# try() keeps the output valid in the brief window right after `terraform import`,
# when output_data is still null (the reconciling apply then populates it).
output "result" { value = try(jsondecode(powershell_script.this.output_data), null) }
