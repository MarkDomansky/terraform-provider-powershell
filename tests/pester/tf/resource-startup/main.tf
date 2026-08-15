terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# Path seeded into a startup-script global and consumed by the resource. The
# Pester test supplies a unique temp path via terraform.tfvars. Scripts manage
# their own globals (here $global:Shared); the persistent runspace keeps them
# alive across every resource operation.
variable "file_path" {
  type = string
}

provider "powershell" {
  startup_script = <<-PS
    $global:Shared = @{ base_path = "${var.file_path}" }
  PS
}

resource "powershell_script" "test" {
  create_script = <<-PS
    $path = $global:Shared.base_path
    New-Item -Path "$path" -ItemType File -Value "from-startup" -Force | Out-Null
    [PSCustomObject]@{ id = "startup-test"; path = $path }
  PS
  read_script   = <<-PS
    [PSCustomObject]@{ id = "startup-test" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "startup-test" }
  PS
  delete_script = <<-PS
    Remove-Item -Path $global:Shared.base_path -Force -ErrorAction SilentlyContinue
  PS
}

output "result" { value = jsondecode(powershell_script.test.output_data) }
