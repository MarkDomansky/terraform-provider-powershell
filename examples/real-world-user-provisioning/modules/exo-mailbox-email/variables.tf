variable "user_principal_name" {
  description = "UPN identifying the mailbox in Exchange Online."
  type        = string
}

variable "email" {
  description = "Primary SMTP address to set on the mailbox."
  type        = string
}
