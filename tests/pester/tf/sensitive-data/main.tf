terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# Exercises the sensitive data channels end to end:
#   - sensitive_provider_data surfaces as $global:ProviderData.SensitiveData;
#   - sensitive_input_data merges into $InputData (redacted from plan output);
#   - a script's reserved 'sensitive' output key splits into
#     sensitive_output_data and never appears in output_data.

variable "secret" {
  type      = string
  sensitive = true
}

provider "powershell" {
  sensitive_provider_data = jsonencode({ api_key = "provider-api-key-value" })
}

resource "powershell_script" "svc" {
  create_script = <<-PS
    [PSCustomObject]@{
      id        = "sensitive-test"
      name      = $InputData.name
      sensitive = @{
        token   = "generated-" + $InputData.secret
        api_key = $global:ProviderData.SensitiveData.api_key
      }
    }
  PS

  read_script = <<-PS
    [PSCustomObject]@{
      id        = $InputData.id
      name      = $InputData.name
      sensitive = @{
        token   = "generated-" + $InputData.secret
        api_key = $global:ProviderData.SensitiveData.api_key
      }
    }
  PS

  update_script = <<-PS
    [PSCustomObject]@{
      id        = $InputData.id
      name      = $InputData.name
      sensitive = @{
        token   = "generated-" + $InputData.secret
        api_key = $global:ProviderData.SensitiveData.api_key
      }
    }
  PS

  delete_script = <<-PS
    # noop
  PS

  input_data           = jsonencode({ name = "svc-1" })
  sensitive_input_data = jsonencode({ secret = var.secret })
}

output "result" { value = jsondecode(powershell_script.svc.output_data) }

output "sensitive_result" {
  value     = jsondecode(powershell_script.svc.sensitive_output_data)
  sensitive = true
}
