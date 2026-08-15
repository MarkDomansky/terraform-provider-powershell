terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# Path to the file the resource creates/reads/deletes. The Pester test supplies a
# unique temp path via terraform.tfvars so runs do not collide.
variable "file_path" {
  type = string
}

provider "powershell" {}

resource "powershell_script" "test" {
  create_script = <<-PS
    New-Item -Path "${var.file_path}" -ItemType File -Value "hello" -Force | Out-Null
    [PSCustomObject]@{ id = "test-file"; path = "${var.file_path}" }
  PS
  read_script   = <<-PS
    if (Test-Path "${var.file_path}") {
      [PSCustomObject]@{ id = "test-file"; path = "${var.file_path}"; content = (Get-Content "${var.file_path}" -Raw) }
    }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "test-file" }
  PS
  delete_script = <<-PS
    Remove-Item -Path "${var.file_path}" -Force -ErrorAction SilentlyContinue
  PS
}

output "result" { value = jsondecode(powershell_script.test.output_data) }
