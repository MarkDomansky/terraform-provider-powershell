terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# Same startup+shutdown contract as tf/provider-scripts, but the provider scripts
# are loaded from real .ps1 files with file() instead of inline heredocs. Because
# terraform reads .ps1 files verbatim (no interpolation), the marker directory is
# handed to the scripts through the provider's provider_data argument and read back
# as $global:ProviderData.Data.marker_dir. The .ps1 files live under scripts/
# so they can be linted as plain PowerShell; the Pester suite copies the whole folder
# into each temp workspace.
variable "marker_dir" {
  type = string
}

provider "powershell" {
  provider_data   = jsonencode({ marker_dir = var.marker_dir })
  startup_script  = file("${path.module}/scripts/startup.ps1")
  shutdown_script = file("${path.module}/scripts/shutdown.ps1")
}

resource "powershell_script" "test" {
  create_script = <<-PS
    $global:Provisioned.Add("res-1")
    [PSCustomObject]@{ id = "provider-scripts-test"; provisioned = $global:Provisioned.Count }
  PS
  read_script   = <<-PS
    [PSCustomObject]@{ id = "provider-scripts-test" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "provider-scripts-test" }
  PS
  delete_script = <<-PS
    # noop
  PS
}

output "result" { value = jsondecode(powershell_script.test.output_data) }
