variable "file_path" {
  description = "Absolute or relative path of the file to manage."
  type        = string
}

variable "content" {
  description = "Content to write to the file."
  type        = string
  default     = ""
}
