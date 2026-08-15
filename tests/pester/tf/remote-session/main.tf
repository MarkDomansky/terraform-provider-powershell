terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# Parameter-driven remote-session probe. Every session_* setting is a string
# variable (empty = "unset") so the Pester suite can drive any single- or
# double-hop scenario from a config file via terraform.tfvars. Empty strings are
# converted to null (provider attrs are optional) and the typed values
# (port/use_ssl) are parsed here, which keeps the tfvars writer string-only.
#
# Single hop:  the provider opens one session (winrm/ssh/vmguest) to session_host
#              and the probe reports the hostname it landed on.
# Double hop:  set hop2_* and the probe, already running on hop1, opens a further
#              PSSession to hop2 and reports that hostname too. Doing the second hop
#              with explicit fresh credentials avoids the WinRM "double-hop"
#              delegation problem.

variable "session_type" { type = string }
variable "session_host" {
  type    = string
  default = ""
}
variable "session_port" {
  type    = string
  default = ""
}
variable "session_username" {
  type    = string
  default = ""
}
variable "session_password" {
  type    = string
  default = ""
}
variable "session_use_ssl" {
  type    = string
  default = ""
}
variable "session_authentication" {
  type    = string
  default = ""
}
variable "session_cert_thumbprint" {
  type    = string
  default = ""
}
variable "session_configuration_name" {
  type    = string
  default = ""
}
variable "session_key_file" {
  type    = string
  default = ""
}
variable "session_vm_name" {
  type    = string
  default = ""
}
variable "session_vm_id" {
  type    = string
  default = ""
}

# Optional second hop (driven by the probe script, not by the provider session).
# hop2_type selects the transport: "winrm" (default, password credential) or "ssh"
# (key-file auth, e.g. a Linux hop1 reaching a Windows host). hop2_key_file is a path
# ON hop1.
variable "hop2_type" {
  type    = string
  default = ""
}
variable "hop2_host" {
  type    = string
  default = ""
}
variable "hop2_username" {
  type    = string
  default = ""
}
variable "hop2_password" {
  type    = string
  default = ""
}
variable "hop2_key_file" {
  type    = string
  default = ""
}
variable "hop2_port" {
  type    = string
  default = ""
}

provider "powershell" {
  session_type               = var.session_type == "" ? null : var.session_type
  session_host               = var.session_host == "" ? null : var.session_host
  session_port               = var.session_port == "" ? null : tonumber(var.session_port)
  session_username           = var.session_username == "" ? null : var.session_username
  session_password           = var.session_password == "" ? null : var.session_password
  session_use_ssl            = var.session_use_ssl == "" ? null : tobool(var.session_use_ssl)
  session_authentication     = var.session_authentication == "" ? null : var.session_authentication
  session_cert_thumbprint    = var.session_cert_thumbprint == "" ? null : var.session_cert_thumbprint
  session_configuration_name = var.session_configuration_name == "" ? null : var.session_configuration_name
  session_key_file           = var.session_key_file == "" ? null : var.session_key_file
  session_vm_name            = var.session_vm_name == "" ? null : var.session_vm_name
  session_vm_id              = var.session_vm_id == "" ? null : var.session_vm_id
}

resource "powershell_script" "probe" {
  create_script = file("${path.module}/scripts/create.ps1")
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")

  # The second hop is performed inside the script, so its target/credentials are
  # passed through input_data rather than the provider session.
  input_data = jsonencode({
    hop2_type     = var.hop2_type
    hop2_host     = var.hop2_host
    hop2_username = var.hop2_username
    hop2_password = var.hop2_password
    hop2_key_file = var.hop2_key_file
    hop2_port     = var.hop2_port
  })
}

output "probe" { value = jsondecode(powershell_script.probe.output_data) }
