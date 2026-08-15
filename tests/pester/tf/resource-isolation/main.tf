terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

provider "powershell" {}

# Resource A deliberately leaves a bare, unscoped variable behind in every CRUD
# script. If resource scripts shared one runspace scope, this would persist in the
# process and be visible to later resources.
resource "powershell_script" "first" {
  create_script = <<-PS
    $leakedSecret = "should-not-escape"
    [PSCustomObject]@{ id = "first" }
  PS
  read_script   = <<-PS
    $leakedSecret = "should-not-escape"
    [PSCustomObject]@{ id = "first" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "first" }
  PS
  delete_script = "# noop"
}

# Resource B is sequenced after A via depends_on, then tries to read the variable
# A left behind. Because CRUD scripts run in an isolated child scope, $leakedSecret
# is undefined here, so "seen" comes back empty rather than "should-not-escape".
resource "powershell_script" "second" {
  depends_on = [powershell_script.first]

  create_script = <<-PS
    [PSCustomObject]@{ id = "second"; seen = "$leakedSecret" }
  PS
  read_script   = <<-PS
    [PSCustomObject]@{ id = "second"; seen = "$leakedSecret" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "second"; seen = "$leakedSecret" }
  PS
  delete_script = "# noop"
}

# Empty when isolation holds; "should-not-escape" if a script's variables leak.
output "second_seen" {
  value = jsondecode(powershell_script.second.output_data).seen
}
