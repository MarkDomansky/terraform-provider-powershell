terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# Verifies the provider-level `timeout` argument is actually enforced. The create
# script sleeps far longer than the timeout, so the host must force-kill the
# PowerShell sidecar and fail the apply with a timeout error rather than blocking
# for the whole sleep. The timeout is generous enough (8s) that the one-time
# provider configure round-trip always completes, so only the long-running script
# trips it.
provider "powershell" {
  timeout = 8
}

resource "powershell_script" "slow" {
  create_script = <<-PS
    Start-Sleep -Seconds 60
    [PSCustomObject]@{ id = "slow" }
  PS
  read_script   = <<-PS
    [PSCustomObject]@{ id = "slow" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "slow" }
  PS
  delete_script = <<-PS
    # noop
  PS
}

output "result" { value = jsondecode(powershell_script.slow.output_data) }
