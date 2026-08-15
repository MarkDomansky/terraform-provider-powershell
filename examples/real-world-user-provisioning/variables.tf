variable "first_name" {
  description = "User's given name."
  type        = string
}

variable "last_name" {
  description = "User's surname."
  type        = string
}

variable "department" {
  description = "Department recorded on the user object."
  type        = string
  default     = "Engineering"
}

variable "domain" {
  description = "UPN / primary-SMTP domain, e.g. contoso.com."
  type        = string
}

# --- Active Directory connection ---

variable "domain_controller" {
  description = "Hostname of the domain controller to target."
  type        = string
}

variable "base_ou" {
  description = "Distinguished name of the OU new objects are created in, e.g. OU=Staff,DC=contoso,DC=com."
  type        = string
}

variable "ad_admin_username" {
  description = "Service account used to manage AD objects."
  type        = string
}

variable "ad_admin_password" {
  description = "Service account password. Supply via TF_VAR_ad_admin_password, not a checked-in tfvars file."
  type        = string
  sensitive   = true
}

# --- Exchange Online (app-only) connection ---

variable "exo_app_id" {
  description = "Entra app registration (client) ID used for app-only EXO auth."
  type        = string
}

variable "exo_cert_thumbprint" {
  description = "Thumbprint of the certificate registered for the EXO app."
  type        = string
}

variable "exo_organization" {
  description = "EXO organization, e.g. contoso.onmicrosoft.com."
  type        = string
}
