---
page_title: "powershell_script Data Source"
description: |-
  Reads data through a PowerShell script executed during refresh.
---

# powershell_script (Data Source)

Reads data through a PowerShell script executed during refresh — the PowerShell
counterpart of the `hashicorp/external` data source. The script runs in the same
persistent PowerShell process (and remote session, if configured) as every
resource, so it can use `$global:ProviderData` and any globals seeded by
`startup_script`.

The script must be **read-only**: it runs on every refresh and plan, so it must
not change any system state.

## Example Usage

```hcl
data "powershell_script" "domain_controllers" {
  script = <<-PS
    $dcs = Get-ADDomainController -Filter * -Server $InputData.domain |
      Select-Object -ExpandProperty HostName
    [PSCustomObject]@{ hostnames = $dcs }
  PS

  input_data = jsonencode({ domain = var.domain })
}

output "dc_hostnames" {
  value = jsondecode(data.powershell_script.domain_controllers.output_data).hostnames
}
```

## Script Contract

The same contract as [the `powershell_script` resource](../resources/script.md#script-contract),
with two differences:

- The script runs with `$Action` set to `"read"`.
- Emitting **nothing** is valid and yields `output_data = "{}"` (a data source
  has no state to remove).

Emit at most one object; a value under the reserved top-level `sensitive` key is
split into `sensitive_output_data`.

## Schema

### Required

- `script` (String) - PowerShell script executed to read data. Must emit at most one object.

### Optional

- `input_data` (String, JSON) - JSON-encoded input data delivered to the script through the bound `$InputData` parameter. Use `jsonencode()` to construct. Must be a JSON object.
- `sensitive_input_data` (String, JSON, **Sensitive**) - JSON-encoded sensitive input data, merged into `$InputData` after `input_data` (on key collision the sensitive value wins). Redacted from plan output.
- `timeout` (Number) - Timeout in seconds (minimum 1). Overrides the provider-level default.

### Read-Only

- `output_data` (String, JSON) - JSON-encoded form of the object the script emitted, excluding the reserved `sensitive` key. Use `jsondecode()` to extract values.
- `sensitive_output_data` (String, JSON, **Sensitive**) - JSON-encoded form of the value emitted under the reserved `sensitive` key (`"{}"` when absent). Redacted from plan output.
