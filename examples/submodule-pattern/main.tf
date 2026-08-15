# Submodule Pattern Example
#
# This demonstrates the recommended pattern: each managed resource type
# is a Terraform module that bundles its CRUD scripts with typed variables.

terraform {
  required_providers {
    powershell = {
      source = "registry.terraform.io/markdomansky/powershell"
    }
  }
}

provider "powershell" {
  startup_script = <<-PS
    $global:ProviderState = @{}
    # Provider-level initialization: load shared modules, authenticate, etc.
    $global:ProviderState["environment"] = "development"
  PS
}

# Use the managed-file submodule to create files declaratively
module "config_file" {
  source = "./modules/managed-file"

  file_path = "${path.root}/output/config.json"
  content   = jsonencode({
    setting1 = "value1"
    setting2 = "value2"
    env      = "dev"
  })
}

module "readme_file" {
  source = "./modules/managed-file"

  file_path = "${path.root}/output/README.txt"
  content   = "This directory is managed by Terraform PowerShell Provider."
}

output "config_file_id" {
  value = module.config_file.resource_id
}

output "readme_file_id" {
  value = module.readme_file.resource_id
}
