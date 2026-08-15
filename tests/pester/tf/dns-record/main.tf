terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# DNS A-record integration config. The provider opens a WinRM session to a Windows
# DNS server and the CRUD scripts manage a real A record there with the DnsServer
# module (Add/Get/Remove-DnsServerResourceRecord). All session_* and DNS values are
# string variables driven from the Pester config file via terraform.tfvars.
#
# The record id is "<server>/<zone>/<name>", which is self-describing: the read
# script can rebuild full state from the id alone, so `terraform import` can restore
# a lost state from the TF file.

variable "session_type" {
  type    = string
  default = "winrm"
}
variable "session_host" { type = string }
variable "session_username" { type = string }
variable "session_password" { type = string }
variable "session_authentication" {
  type    = string
  default = "Negotiate"
}
variable "session_use_ssl" {
  type    = string
  default = ""
}

# DNS record settings.
variable "dns_server" {
  type    = string
  default = "localhost"
}
variable "zone" { type = string }
variable "record_name" { type = string }
variable "ipv4_address" { type = string }

provider "powershell" {
  session_type           = var.session_type
  session_host           = var.session_host
  session_username       = var.session_username
  session_password       = var.session_password
  session_authentication = var.session_authentication
  session_use_ssl        = var.session_use_ssl == "" ? null : tobool(var.session_use_ssl)
}

resource "powershell_script" "record" {
  create_script = file("${path.module}/scripts/create.ps1")
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")

  input_data = jsonencode({
    dns_server   = var.dns_server
    zone         = var.zone
    record_name  = var.record_name
    ipv4_address = var.ipv4_address
  })
}

# try() keeps the output valid in the brief window right after `terraform import`,
# when output_data is still null (the reconciling apply then populates it).
output "record" { value = try(jsondecode(powershell_script.record.output_data), null) }
