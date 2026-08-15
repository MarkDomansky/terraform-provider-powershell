terraform {
  required_providers {
    powershell = {
      source = "registry.terraform.io/markdomansky/powershell"
    }
  }
}

# -----------------------------------------------------------------------------
# powershell_script data source
#
# A read-only script executed during refresh — the PowerShell counterpart of the
# hashicorp/external data source. It runs in the provider's persistent process,
# so it can use $global:ProviderData and globals seeded by startup_script.
# The script must not change any system state.
# -----------------------------------------------------------------------------

provider "powershell" {}

data "powershell_script" "os_info" {
  script = <<-PS
    $os = [System.Environment]::OSVersion
    [PSCustomObject]@{
      platform = "$($os.Platform)"
      version  = "$($os.Version)"
      machine  = [System.Environment]::MachineName
    }
  PS
}

# Feed data source results into a resource, exactly like any other data source.
resource "powershell_script" "report" {
  create_script = <<-PS
    [PSCustomObject]@{
      id      = "report-1"
      summary = "running on $($InputData.machine) ($($InputData.platform))"
    }
  PS
  read_script   = "[PSCustomObject]@{ id = 'report-1' }"
  delete_script = "# nothing to clean up"

  input_data = data.powershell_script.os_info.output_data
}

output "os_info" {
  value = jsondecode(data.powershell_script.os_info.output_data)
}

output "summary" {
  value = jsondecode(powershell_script.report.output_data).summary
}
