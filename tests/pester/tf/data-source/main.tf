terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# Exercises the powershell_script data source: merged (sensitive) input,
# provider-global visibility, the reserved 'sensitive' output split, and the
# empty-output contract (a data source emitting nothing yields "{}").

provider "powershell" {
  startup_script = <<-PS
    $global:DSState = @{ farm = "farm-a" }
  PS
}

data "powershell_script" "info" {
  script = <<-PS
    [PSCustomObject]@{
      combined  = "$($InputData.prefix)-$($InputData.secret_suffix)"
      farm      = $global:DSState.farm
      action    = $Action
      sensitive = @{ secret_suffix = $InputData.secret_suffix }
    }
  PS

  input_data           = jsonencode({ prefix = "app" })
  sensitive_input_data = jsonencode({ secret_suffix = "hush" })
}

data "powershell_script" "empty" {
  script = <<-PS
    # emits nothing on purpose
  PS
}

output "info" { value = jsondecode(data.powershell_script.info.output_data) }

output "info_secret" {
  value     = jsondecode(data.powershell_script.info.sensitive_output_data)
  sensitive = true
}

output "empty" { value = data.powershell_script.empty.output_data }
