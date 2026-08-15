terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# Default configuration: seeds a secret and an 'owner' key only it ever sets.
provider "powershell" {
  startup_script = <<-PS
    $global:Shared = @{ secret = "alpha-secret"; owner = "default" }
  PS
}

# Aliased configuration: a fully independent runspace with its own secret.
provider "powershell" {
  alias          = "beta"
  startup_script = <<-PS
    $global:Shared = @{ secret = "beta-secret" }
  PS
}

resource "powershell_script" "a" {
  create_script = <<-PS
    [PSCustomObject]@{
      id     = "a"
      pid    = "$PID"
      secret = "$($global:Shared.secret)"
      owner  = "$($global:Shared.owner)"
    }
  PS
  read_script   = <<-PS
    [PSCustomObject]@{ id = "a" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "a" }
  PS
  delete_script = <<-PS
    # noop
  PS
}

resource "powershell_script" "b" {
  provider      = powershell.beta
  create_script = <<-PS
    [PSCustomObject]@{
      id     = "b"
      pid    = "$PID"
      secret = "$($global:Shared.secret)"
      owner  = "$($global:Shared.owner)"
    }
  PS
  read_script   = <<-PS
    [PSCustomObject]@{ id = "b" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "b" }
  PS
  delete_script = <<-PS
    # noop
  PS
}

output "a" { value = jsondecode(powershell_script.a.output_data) }
output "b" { value = jsondecode(powershell_script.b.output_data) }
