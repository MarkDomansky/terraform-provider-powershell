terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

provider "powershell" {}

resource "powershell_script" "resource_a" {
  create_script = <<-PS
    [PSCustomObject]@{ id = "res-a"; pid = "$PID" }
  PS
  read_script   = <<-PS
    [PSCustomObject]@{ id = "res-a"; pid = "$PID" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "res-a" }
  PS
  delete_script = <<-PS
    # noop
  PS
}

resource "powershell_script" "resource_b" {
  create_script = <<-PS
    [PSCustomObject]@{ id = "res-b"; pid = "$PID" }
  PS
  read_script   = <<-PS
    [PSCustomObject]@{ id = "res-b"; pid = "$PID" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "res-b" }
  PS
  delete_script = <<-PS
    # noop
  PS
}

output "a" { value = jsondecode(powershell_script.resource_a.output_data) }
output "b" { value = jsondecode(powershell_script.resource_b.output_data) }
