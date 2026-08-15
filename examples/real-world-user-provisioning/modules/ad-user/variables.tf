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
  description = "UPN domain, e.g. contoso.com."
  type        = string
}
