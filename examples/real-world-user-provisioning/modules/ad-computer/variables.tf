variable "computer_name" {
  description = "NetBIOS name of the computer account (max 15 characters)."
  type        = string

  validation {
    condition     = length(var.computer_name) <= 15
    error_message = "computer_name must be 15 characters or fewer (NetBIOS limit)."
  }
}

variable "owner_dn" {
  description = "Distinguished name of the user the workstation is assigned to (ManagedBy)."
  type        = string
}

variable "owner_display_name" {
  description = "Display name of the owner, used in the computer's description."
  type        = string
}
