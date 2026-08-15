terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

provider "powershell" {}

resource "powershell_script" "json_test" {
  create_script = <<-PS
    [PSCustomObject]@{
      id       = "json-test"
      greeting = "Hello, $($InputData.name)!"
      count    = [int]$InputData.count * 2
    }
  PS
  read_script   = <<-PS
    [PSCustomObject]@{ id = "json-test" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "json-test" }
  PS
  delete_script = <<-PS
    # nothing to clean up
  PS

  input_data = jsonencode({ name = "Terraform", count = 21 })
}

output "result" { value = jsondecode(powershell_script.json_test.output_data) }
