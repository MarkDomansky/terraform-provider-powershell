# Real-World Example: Provision an AD user, a computer account assigned to that
# user, and the user's Exchange Online mailbox address.
#
# Each object type is its own submodule that bundles its CRUD scripts. The root
# module just wires them together, so the data flow is obvious at a glance:
#
#   module.user  ──▶ module.computer       (ManagedBy = the user's DN)
#               └──▶ module.mailbox_email  (runs against the "exo" provider alias)

terraform {
  required_providers {
    powershell = {
      source = "registry.terraform.io/markdomansky/powershell"
    }
  }
}

# Provider 1 — on-prem Active Directory (default configuration).
provider "powershell" {
  startup_script = <<-PS
    $global:ProviderState = @{}
    Import-Module ActiveDirectory

    # Service-account credential, interpolated from sensitive Terraform variables.
    $user = '${var.ad_admin_username}'
    $pass = ConvertTo-SecureString '${var.ad_admin_password}' -AsPlainText -Force

    $global:ProviderState["credential"] = [System.Management.Automation.PSCredential]::new($user, $pass)
    $global:ProviderState["server"]     = '${var.domain_controller}'
    $global:ProviderState["base_ou"]    = '${var.base_ou}'
  PS
}

# Provider 2 — Exchange Online (aliased), via the ExchangeOnlineManagement module.
provider "powershell" {
  alias = "exo"

  startup_script = <<-PS
    $global:ProviderState = @{}
    Import-Module ExchangeOnlineManagement

    # App-only certificate auth keeps the session non-interactive (CI-friendly).
    Connect-ExchangeOnline `
      -AppId                 '${var.exo_app_id}' `
      -CertificateThumbprint '${var.exo_cert_thumbprint}' `
      -Organization          '${var.exo_organization}' `
      -ShowBanner:$false

    $global:ProviderState["organization"] = '${var.exo_organization}'
  PS

  shutdown_script = <<-PS
    # Runs once, after the last resource on this alias. Close the EXO session.
    Disconnect-ExchangeOnline -Confirm:$false
  PS
}

# 1) The user — source of truth for everything downstream.
module "user" {
  source = "./modules/ad-user"

  first_name = var.first_name
  last_name  = var.last_name
  department = var.department
  domain     = var.domain
}

# 2) A workstation assigned to the user. owner_dn references module.user's output,
#    so Terraform creates the user first and the computer after.
module "computer" {
  source = "./modules/ad-computer"

  # NetBIOS computer names are capped at 15 characters.
  computer_name      = substr(upper("WS-${var.first_name}${var.last_name}"), 0, 15)
  owner_dn           = module.user.distinguished_name
  owner_display_name = module.user.display_name
}

# 3) The user's Exchange Online mailbox address. The module declares an "exo"
#    provider slot; bind it here to the aliased configuration above.
module "mailbox_email" {
  source = "./modules/exo-mailbox-email"

  providers = {
    powershell.exo = powershell.exo
  }

  user_principal_name = module.user.user_principal_name
  email               = module.user.user_principal_name
}

output "user_upn" {
  value = module.user.user_principal_name
}

output "computer_dn" {
  value = module.computer.distinguished_name
}

output "mailbox_primary_smtp" {
  value = module.mailbox_email.primary_smtp
}
