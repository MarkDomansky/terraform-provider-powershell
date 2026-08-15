terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# Exercises the update-vs-replace semantics:
#   - mutable has an update_script: input changes run it in place, and script
#     edits (the interpolated note comment) are config-only updates that
#     execute nothing against the "target system" (the marker logs).
#   - immutable has no update_script: input changes force a replacement.
# The scripts append to marker logs so the suite can count how often each
# lifecycle action actually executed.

variable "marker_dir" {
  type = string
}

variable "content" {
  type = string
}

variable "note" {
  type    = string
  default = "rev1"
}

provider "powershell" {}

resource "powershell_script" "mutable" {
  create_script = <<-PS
    # note: ${var.note}
    Add-Content -Path "${var.marker_dir}/mutable-create.log" -Value "create"
    [PSCustomObject]@{ id = "mutable-1"; content = $InputData.content }
  PS

  read_script = <<-PS
    [PSCustomObject]@{ id = $InputData.id; content = $InputData.content }
  PS

  update_script = <<-PS
    Add-Content -Path "${var.marker_dir}/mutable-update.log" -Value "update"
    [PSCustomObject]@{ id = $InputData.id; content = $InputData.content }
  PS

  delete_script = <<-PS
    # noop
  PS

  input_data = jsonencode({ content = var.content })
}

resource "powershell_script" "immutable" {
  create_script = <<-PS
    Add-Content -Path "${var.marker_dir}/immutable-create.log" -Value "create"
    [PSCustomObject]@{ id = "immutable-1"; content = $InputData.content }
  PS

  read_script = <<-PS
    [PSCustomObject]@{ id = $InputData.id; content = $InputData.content }
  PS

  delete_script = <<-PS
    # noop
  PS

  input_data = jsonencode({ content = var.content })
}

output "mutable" { value = jsondecode(powershell_script.mutable.output_data) }

output "immutable" { value = jsondecode(powershell_script.immutable.output_data) }
