terraform {
  required_providers {
    powershell = {
      source = "registry.terraform.io/markdomansky/powershell"
    }
  }
}

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
