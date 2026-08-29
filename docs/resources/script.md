---
page_title: "powershell_script Resource"
description: |-
  Manages a resource through PowerShell CRUD scriptblocks.
---

# powershell_script

Manages a resource through PowerShell CRUD scriptblocks. Each lifecycle action (create, read, update, delete) is handled by a separate PowerShell script. Scripts receive input through a bound `$InputData` parameter and return output by emitting exactly one object to the PowerShell output (success) stream. Inputs and the emitted object are exchanged as JSON.

~> For a script that only **looks something up** — no lifecycle, no state, no
drift — use the [`powershell_script` data source](../data-sources/script.md)
instead.

## Example Usage

```hcl
resource "powershell_script" "example" {
  create_script = file("${path.module}/scripts/create.ps1")
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")

  input_data = jsonencode({
    name     = "my-resource"
    location = "eastus"
  })

  triggers = {
    script_hash = sha256(file("${path.module}/scripts/create.ps1"))
  }
}

output "resource_output" {
  value = jsondecode(powershell_script.example.output_data)
}
```

## Script Contract

A CRUD script receives its inputs through a **bound `$InputData` parameter** — the host
injects a `param($InputData)` block automatically, so scripts must **not** declare their
own `param(...)` block. A script returns its result by **emitting exactly one object to
the output (success) stream**, typically `[PSCustomObject]@{ id = '...'; ... }`. There is
no `$OutputData` variable. The following are available inside any CRUD script:

- `$InputData` (Hashtable) - Bound parameter parsed from `input_data` JSON. On read/update/delete operations, also includes the resource `id`.
- `$global:ProviderData` (Hashtable) - Read-only connection arguments configured on the provider block (`server`, `username`, `password`, `cert_thumbprint`, the decoded `provider_data` argument as `$global:ProviderData.Data`, and the decoded `sensitive_provider_data` argument as `$global:ProviderData.SensitiveData`). Always present (empty if none were set).
- Your own `$global:*` variables - Any global a script creates persists across all operations in the Terraform run (the runspace is persistent). Initialize them in `startup_script`; the provider does not pre-create a state bag.
- `$Action` (String) - Current lifecycle action: `"create"`, `"read"`, `"update"`, or `"delete"`.

**Emitting output:**

- Emit **exactly one** object to the output stream. The create script's emitted object **must** have an `id` field whose value is a non-empty string.
- A read script that finds the resource gone should emit **nothing** — empty output tells Terraform to remove it from state.
- If a script emits **more than one** object (e.g. leaked cmdlet output), the operation **fails**. Pipe stray cmdlet output to `Out-Null` so only your result object reaches the stream.
- All other PowerShell streams (Information/Warning/Verbose/Debug) are captured for diagnostics, and **any record written to the error stream marks the operation as failed**, even without a thrown terminating error.

## Passing variables to script files

Inline (heredoc) scripts can interpolate Terraform values directly with
`${var.name}`, because Terraform renders the string before handing it to the
provider. **Script files loaded with `file()` are read verbatim** — Terraform
does *not* interpolate their contents, so `${var.name}` inside a `.ps1` file
reaches PowerShell unchanged. Pass values into file-based scripts through
`input_data` instead, and read them from the `$InputData` parameter:

```hcl
resource "powershell_script" "managed_file" {
  create_script = file("${path.module}/scripts/create.ps1")
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")

  # Terraform variables are serialized to JSON here...
  input_data = jsonencode({
    file_path = var.file_path
    content   = var.content
  })

  triggers = {
    content_hash = sha256(var.content)
  }
}
```

```powershell
# scripts/create.ps1 - reads the values from the $InputData parameter
$filePath = $InputData.file_path
$content  = $InputData.content

Set-Content -Path $filePath -Value $content -NoNewline

[PSCustomObject]@{
  id        = $filePath
  file_path = $filePath
  size      = (Get-Item $filePath).Length
}
```

On `read`, `update`, and `delete`, `$InputData` also carries the resource
`id`, so a file-based script can locate the resource it manages without any extra
plumbing.

Provider-wide values shared by every resource (a subscription id, an
authenticated session) come from `startup_script` via a global you create (e.g.
`$global:ProviderState`) rather than `input_data` — see
[Sharing state across multiple resources](../index.md#sharing-state-across-multiple-resources).
Typed connection details (server, credentials, …) are also available read-only as
[`$global:ProviderData`](../index.md#using-connection-arguments).

> **When you really need interpolation in a file**, use `templatefile()` instead
> of `file()` to render Terraform values into the script before it runs:
> `create_script = templatefile("${path.module}/scripts/create.ps1.tftpl", { name = var.name })`.
> Prefer `input_data` for ordinary values — it keeps scripts as plain, testable
> `.ps1` files and avoids embedding data (including secrets) into the rendered
> script stored in state.

## Accessing the existing object (read, update, delete)

During the **R**, **U**, and **D** operations the provider does **not** hand the
script the full object it created earlier. The only piece of the existing object
carried over automatically is its **`id`** — the value from the `id` field of the
object the create script emitted. The provider injects that id into `$InputData.id`
so the script can locate the real-world object and look up whatever else it
needs.

What else `$InputData` contains differs by operation:

| Operation | `$InputData.id` | Other `$InputData` keys          | Sourced from    |
|-----------|-----------------|----------------------------------|-----------------|
| `read`    | existing id     | the resource's `input_data`      | prior **state** |
| `update`  | existing id     | the **desired new** `input_data` | the **plan**    |
| `delete`  | existing id     | the resource's `input_data`      | prior **state** |

> The previously computed `output_data` is stored in Terraform state but is **not**
> passed back into scripts. If a read/update script needs the object's current
> real-world values, it must fetch them using `id` (as below) — or read shared
> values from a global seeded in `startup_script`.

### Read — locate the object by `id` and report its current state

```powershell
# read.ps1
$id = $InputData.id          # e.g. the file path the create script returned

if (Test-Path $id) {
  [PSCustomObject]@{
    id      = $id
    content = Get-Content $id -Raw
    size    = (Get-Item $id).Length
  }
}
# Emitting nothing tells Terraform the object is gone, so it plans to recreate it.
```

### Update — `id` is the existing object, other keys are the *desired* new values

On update, `$InputData` carries the **planned** (new) `input_data` plus the
existing `id`. The prior input/output is **not** supplied; if you need to diff
against the live object, read it by `id` first.

```powershell
# update.ps1
$id         = $InputData.id        # existing identity, preserved from state
$newContent = $InputData.content   # desired new value from the config/plan

# (optional) inspect the current object before changing it
$previous = if (Test-Path $id) { Get-Content $id -Raw } else { $null }

Set-Content -Path $id -Value $newContent -NoNewline

[PSCustomObject]@{
  id       = $id
  content  = $newContent
  size     = (Get-Item $id).Length
  replaced = $previous -ne $newContent
}
```

### Delete — `id` identifies the object to destroy

```powershell
# delete.ps1
$id = $InputData.id
Remove-Item -Path $id -Force -ErrorAction SilentlyContinue
# No output needed; on success Terraform drops the resource from state.
```

Because `id` is the durable handle across the resource's lifetime, the create
script should set it to something the read/update/delete scripts can resolve back
to the real object — a primary key, ARN, path, or other stable identifier rather
than an opaque, unreconstructable value.

## Update, replace, and script changes

- Changing **`input_data` or `sensitive_input_data`** runs `update_script` in
  place. When `update_script` is omitted, the resource is treated as immutable
  and input changes force a **replacement** (delete then create).
- Changing a **script attribute** (`create_script`, `read_script`,
  `update_script`, `delete_script`) or `timeout` is a config-only update:
  nothing executes against the target system and the previous outputs are kept.
  Scripts describe *how* to manage the object, not *what* it is.
- Changing a **`triggers`** entry forces a replacement. Use it when a script
  change should recreate the object, e.g.
  `triggers = { script_hash = sha256(file("create.ps1")) }`.

## Schema

### Required

- `create_script` (String) - PowerShell script for resource creation. Must emit exactly one object whose `id` field is a non-empty string. Changes do **not** force replacement; use `triggers` for that.
- `read_script` (String) - PowerShell script to read current resource state. Must emit exactly one object with current values. Emit nothing if the resource no longer exists.
- `delete_script` (String) - PowerShell script for resource deletion.

### Optional

- `update_script` (String) - PowerShell script for in-place updates, run when `input_data` or `sensitive_input_data` change. Must emit exactly one object with the updated state. When omitted, input changes force a replacement.
- `input_data` (String, JSON) - JSON-encoded input data delivered to scripts through the bound `$InputData` parameter. Use `jsonencode()` to construct. Must be a JSON object.
- `sensitive_input_data` (String, JSON, **Sensitive**) - JSON-encoded sensitive input data, merged into `$InputData` after `input_data` (on key collision the sensitive value wins). Redacted from plan output; still stored in state.
- `triggers` (Map of String) - Map of values that force resource recreation when changed. Useful for tracking script content or configuration hashes.
- `timeout` (Number) - Per-resource timeout in seconds (minimum 1). Overrides the provider-level default.

### Read-Only

- `id` (String) - Resource identifier taken from the `id` field of the object the create script emits.
- `output_data` (String, JSON) - JSON-encoded form of the single object the most recent script emitted, excluding the reserved `sensitive` key. Use `jsondecode()` to extract values.
- `sensitive_output_data` (String, JSON, **Sensitive**) - JSON-encoded form of the value the script emitted under the reserved top-level `sensitive` key (`"{}"` when absent). Redacted from plan output; still stored in state.

## Sensitive data

`input_data` and `output_data` are **not** marked sensitive, so their values appear
in plan output and are stored in plaintext in Terraform state. This is deliberate:
the JSON blobs are the primary diff signal this resource surfaces. Route secrets
through the parallel sensitive channels instead:

- **Sensitive inputs** go in `sensitive_input_data`. Scripts see one merged
  `$InputData`; Terraform redacts the attribute from plan output.

  ```hcl
  resource "powershell_script" "svc" {
    # ...
    input_data           = jsonencode({ name = "svc-account" })
    sensitive_input_data = jsonencode({ password = var.svc_password })
  }
  ```

- **Sensitive outputs** go under the reserved `sensitive` key of the emitted
  object; the provider splits them into `sensitive_output_data`:

  ```powershell
  [PSCustomObject]@{
    id        = $name
    sensitive = @{ connection_string = $connString }
  }
  ```

  ```hcl
  output "conn" {
    value     = jsondecode(powershell_script.svc.sensitive_output_data).connection_string
    sensitive = true
  }
  ```

Both sensitive attributes are redacted from CLI output but **still stored in
Terraform state** — keep state encrypted/remote. To keep a secret out of state
entirely, read it inside scripts from the provider process environment
(`$env:VAR`) or from a global seeded in `startup_script`; neither path touches
resource state. See
[Passing credentials to the startup script](../index.md#passing-credentials-to-the-startup-script).

## Import

Resources can be imported by ID. After import, a read operation runs to populate state:

```bash
terraform import powershell_script.example my-resource-id
```

The read script receives the imported ID via `$InputData.id`.
