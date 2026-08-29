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

## When to use it

Use the data source when a script only **looks things up**; use the
[`powershell_script` resource](../resources/script.md) when Terraform should
**own** an object's lifecycle.

| You want to… | Use |
|---|---|
| Discover facts about a system Terraform does not manage | data source |
| Look up an existing object to reference (a group's SID, a site's ID, a mailbox GUID) | data source |
| Derive a value from the live environment (next free UID, current schema version) | data source |
| Create / update / delete an object, with drift detection | resource |
| Read something once at create time and freeze it in state | resource |

A data source holds no state and has no `id`. Terraform never plans a change for
it, never destroys it, and never reports drift on it — it simply re-reads the
current value on every plan.

## Script Contract

The same contract as [the `powershell_script` resource](../resources/script.md#script-contract),
with two differences:

- The script runs with `$Action` set to `"read"`.
- Emitting **nothing** is valid and yields `output_data = "{}"` (a data source
  has no state to remove).

Everything else carries over:

- The host injects a bound `$InputData` parameter — do **not** write your own
  `param(...)` block.
- Emit **at most one** object to the output (success) stream. Emitting more than
  one fails the read; pipe stray cmdlet output to `Out-Null`.
- Any record written to the **error stream** fails the read, even without a
  `throw`.
- A value under the reserved top-level `sensitive` key is split into
  `sensitive_output_data`.
- `$global:ProviderData` and any globals created by `startup_script` are
  available — `startup_script` always runs during provider configuration, which
  Terraform completes before it reads any data source.

## When the script runs

The script executes during the refresh phase of **every** `plan` and `apply`, and
its result is not persisted between runs. Two consequences:

- **Side effects are run on every plan.** A `plan` that someone runs to review a
  change will execute this script. Keep it read-only and cheap.
- **A changed result shows up as a downstream diff.** If a resource's
  `input_data` references `data.powershell_script.x.output_data` and the script
  returns something new, the next plan proposes an update to that resource. This
  is how external reality enters your plan.

If the data source's `input_data` depends on an attribute of a resource that has
not been created yet, Terraform defers the read until apply, and any value
derived from `output_data` is `(known after apply)` in the plan.

Reads are serialized with every other operation on the same provider — one
mutex per provider sidecar — so a slow lookup blocks resource work. Use an
aliased provider to isolate an expensive read.

## Feeding a resource

`output_data` is a JSON string in the same shape `input_data` expects, so it can
be passed straight through:

```hcl
data "powershell_script" "site" {
  script     = file("${path.module}/scripts/lookup-site.ps1")
  input_data = jsonencode({ name = var.site_name })
}

resource "powershell_script" "server" {
  create_script = file("${path.module}/scripts/create.ps1")
  read_script   = file("${path.module}/scripts/read.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")

  input_data = jsonencode({
    hostname = var.hostname
    site_id  = jsondecode(data.powershell_script.site.output_data).site_id
  })
}
```

Referencing the data source's output is what creates the dependency edge, so
Terraform reads the site before it creates the server.

## Handling secrets

Put lookup secrets in `sensitive_input_data` and return secrets under the
reserved `sensitive` key:

```hcl
data "powershell_script" "api_key" {
  script = <<-PS
    $key = Get-VaultSecret -Name $InputData.name -Token $InputData.token
    [PSCustomObject]@{
      name      = $InputData.name
      sensitive = @{ value = $key }
    }
  PS

  input_data           = jsonencode({ name = "billing-api" })
  sensitive_input_data = jsonencode({ token = var.vault_token })
}

resource "powershell_script" "client" {
  # ...
  sensitive_input_data = jsonencode({
    api_key = jsondecode(data.powershell_script.api_key.sensitive_output_data).value
  })
}
```

`sensitive_input_data` and `sensitive_output_data` are redacted from CLI output
but are **still written to state**, so keep state encrypted. To keep a secret out
of state entirely, read it inside the script from the provider process
environment (`$env:VAR`) or from a global seeded in `startup_script`, and return
only what you actually need.

## Errors and empty results

A data source cannot signal "not found" the way a read script signals a deleted
resource — emitting nothing yields `output_data = "{}"` rather than removing
anything. Decide explicitly which behaviour you want:

```hcl
# Hard failure: the config cannot proceed without the object.
data "powershell_script" "required_group" {
  script = <<-PS
    $g = Get-ADGroup -Identity $InputData.name -ErrorAction SilentlyContinue
    if (-not $g) { throw "Group '$($InputData.name)' does not exist" }
    [PSCustomObject]@{ sid = $g.SID.Value }
  PS

  input_data = jsonencode({ name = "app-admins" })
}

# Soft lookup: report absence as data and branch in HCL.
data "powershell_script" "optional_group" {
  script = <<-PS
    $g = Get-ADGroup -Identity $InputData.name -ErrorAction SilentlyContinue
    [PSCustomObject]@{ found = [bool]$g; sid = if ($g) { $g.SID.Value } else { $null } }
  PS

  input_data = jsonencode({ name = "app-admins" })
}
```

Note that `-ErrorAction SilentlyContinue` matters in the soft case: a
non-terminating error record written to the error stream fails the read.

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
