terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# Exercises BOTH provider-level scripts (startup_script + shutdown_script) and
# proves they share one persistent runspace with the resource operations that run
# between them:
#
#   startup_script  -> seeds a provider-level global ($global:Provisioned) and
#                      writes a startup marker so the test can confirm it ran.
#   create_script   -> appends the resource's id to that same global, proving the
#                      resource sees state the startup script created.
#   shutdown_script -> runs once when the provider tears down (after every
#                      resource op) and writes the accumulated list to a marker
#                      file, proving it observed state built up by startup + create.
#
# The Pester suite supplies a unique, empty temp directory via terraform.tfvars
# and asserts on the marker files after apply.
variable "marker_dir" {
  type = string
}

provider "powershell" {
  startup_script = <<-PS
    $global:Provisioned = [System.Collections.Generic.List[string]]::new()
    "started" | Out-File -FilePath "${var.marker_dir}/startup.txt" -Encoding utf8
  PS

  shutdown_script = <<-PS
    # Runs after all resource operations, in the same persistent process, so it
    # can read the global the startup script created and the resources appended to.
    ($global:Provisioned -join ',') | Out-File -FilePath "${var.marker_dir}/shutdown.txt" -Encoding utf8
  PS
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
