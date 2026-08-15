terraform {
  required_providers {
    powershell = {
      source = "registry.terraform.io/markdomansky/powershell"
    }
  }
}

# -----------------------------------------------------------------------------
# Remote sessions + connection arguments
#
# When session_* arguments are set, the provider opens a remote PowerShell
# session BEFORE the startup script and runs EVERY script (startup, CRUD,
# shutdown) on the remote computer. The typed connection arguments
# (server / username / password / cert_thumbprint / provider_data) are exposed to
# all scripts as the $global:ProviderData hashtable.
#
# NOTE: This example needs a reachable remote host, so it is illustrative. Pick
# ONE provider block below (WinRM or SSH), fill in real values, and remove the
# other. Supply secrets via TF_VAR_* environment variables, not committed files.
# -----------------------------------------------------------------------------

variable "remote_password" {
  type      = string
  sensitive = true
  default   = ""
}

# --- Option A: WinRM ---------------------------------------------------------
provider "powershell" {
  # Connection arguments, surfaced to scripts as $global:ProviderData
  server        = "app-db-01"
  username      = "svc-deploy"
  password      = var.remote_password
  provider_data = jsonencode({ environment = "prod" })

  # Remote session over WinRM (HTTPS / Negotiate auth)
  session_type           = "winrm"
  session_host           = "server01.corp.example.com"
  session_username       = "CORP\\svc-deploy"
  session_password       = var.remote_password
  session_use_ssl        = true
  session_authentication = "Negotiate"

  startup_script = <<-PS
    # Runs ON the remote machine. Create any globals you need here.
    $global:Shared = @{ ran_on = $env:COMPUTERNAME }
  PS
}

# --- Option B: SSH (replace Option A with this) ------------------------------
# provider "powershell" {
#   server        = "app-db-01"
#   provider_data = jsonencode({ environment = "prod" })
#
#   session_type     = "ssh"
#   session_host     = "linux01.example.com"
#   session_username = "deploy"
#   session_key_file = "~/.ssh/id_ed25519"
#
#   startup_script = <<-PS
#     $global:Shared = @{ ran_on = (hostname) }
#   PS
# }

resource "powershell_script" "probe" {
  # These scripts execute on the remote machine. They read the connection
  # arguments from $global:ProviderData and prove where they ran.
  create_script = <<-PS
    [PSCustomObject]@{
      id          = "remote-probe"
      ran_on      = $global:Shared.ran_on
      server      = $global:ProviderData.server
      username    = $global:ProviderData.username
      environment = $global:ProviderData.Data.environment
    }
  PS

  read_script   = <<-PS
    [PSCustomObject]@{ id = "remote-probe" }
  PS

  update_script = <<-PS
    [PSCustomObject]@{ id = "remote-probe" }
  PS

  delete_script = <<-PS
    # nothing to clean up
  PS
}

output "probe" {
  value = jsondecode(powershell_script.probe.output_data)
}
