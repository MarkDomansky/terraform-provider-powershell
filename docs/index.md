---
page_title: "PowerShell Provider"
description: |-
  The PowerShell provider manages resources through PowerShell CRUD scriptblocks.
---

# PowerShell Provider

The PowerShell provider enables Terraform to manage any resource that PowerShell can reach by defining CRUD operations as PowerShell scripts. It maintains a persistent PowerShell process for the lifetime of the Terraform run, enabling provider-level state sharing across all resource operations.

## Requirements

- [Terraform](https://www.terraform.io/downloads) >= 1.0

No PowerShell or .NET installation is required: each provider release bundles a self-contained `pshost` sidecar that embeds PowerShell 7 and the .NET runtime. The provider runs on every OS and architecture Terraform supports (Linux, macOS, and Windows on amd64 and arm64).

## Example Usage

```hcl
provider "powershell" {
  startup_script = <<-PS
    $global:ProviderState = @{}   # your own global; the provider no longer creates one
    Import-Module Az.Accounts
    Connect-AzAccount -Identity
    $global:ProviderState["subscription"] = (Get-AzContext).Subscription.Id
  PS

  shutdown_script = <<-PS
    Disconnect-AzAccount | Out-Null
  PS
}
```

> **State sharing changed:** the provider no longer pre-creates a
> `$global:ProviderState` bag. Scripts manage their own globals instead — the
> PowerShell runspace is persistent for the whole Terraform run, so any
> `$global:*` variable a script creates survives across every later operation.
> Initialize it once in `startup_script` (e.g. `$global:ProviderState = @{}`); the
> name is up to you. Note there is no longer any automatic rollback of globals when
> a script fails.

## Schema

### Optional

- `startup_script` (String) - PowerShell script executed once during provider initialization. Create your own globals (e.g. `$global:ProviderState = @{}`) to store shared state accessible by all resource scripts.
- `shutdown_script` (String) - PowerShell script executed once when the provider tears down, after all resource operations have completed. Runs in the same persistent process (and the same remote session, if configured) as the startup script and every resource, so it can read globals to clean up provider-level state (e.g. sign out, release leases).
- `timeout` (Number) - Default timeout in seconds for script execution. Defaults to 3600 (1 hour).

#### Connection arguments

All optional. Whatever you set is exposed to every script — startup, CRUD and shutdown — as the `$global:ProviderData` hashtable. See [Using connection arguments](#using-connection-arguments).

- `server` (String) - Exposed as `$global:ProviderData.server`.
- `username` (String) - Exposed as `$global:ProviderData.username`.
- `password` (String, **Sensitive**) - Exposed as `$global:ProviderData.password`. Can also be supplied via the `POWERSHELL_PROVIDER_PASSWORD` environment variable — see the security note below.
- `cert_thumbprint` (String) - Exposed as `$global:ProviderData.cert_thumbprint`.
- `provider_data` (String) - Arbitrary data as a JSON object string (use `jsonencode({...})`), decoded to a hashtable and exposed as `$global:ProviderData.Data`.
- `sensitive_provider_data` (String, **Sensitive**) - Arbitrary sensitive data as a JSON object string, decoded to a hashtable and exposed as `$global:ProviderData.SensitiveData`. Redacted from CLI output.

#### Remote session arguments

All optional. Set `session_type` to run every script on a remote computer. See [Remote sessions](#remote-sessions-running-scripts-on-another-machine).

- `session_type` (String) - `winrm`, `ssh`, or `vmguest`. Unset = run locally.
- `session_host` (String) - Remote computer name / hostname (`winrm`, `ssh`).
- `session_port` (Number) - Optional port override.
- `session_username` (String) - Remote session credential username.
- `session_password` (String, **Sensitive**) - Remote session credential password. Can also be supplied via the `POWERSHELL_PROVIDER_SESSION_PASSWORD` environment variable.
- `session_use_ssl` (Bool) - `winrm` only: connect over HTTPS (port 5986).
- `session_authentication` (String) - `winrm` only: `Default`, `Basic`, `Negotiate`, `Kerberos`, `Credssp`, `Digest`, or `NegotiateWithImplicitCredential`.
- `session_cert_thumbprint` (String) - `winrm` only: client-certificate thumbprint (used instead of username/password).
- `session_configuration_name` (String) - `winrm` only: session configuration endpoint, e.g. `PowerShell.7`.
- `session_key_file` (String) - `ssh` only: path to the private key file.
- `session_vm_name` (String) - `vmguest` only: VM name for PowerShell Direct (needs `session_username`/`session_password`).
- `session_vm_id` (String) - `vmguest` only: VM GUID for PowerShell Direct (alternative to `session_vm_name`).

## Passing credentials to the startup script

The provider has no dedicated credential argument — the startup script is just a
PowerShell string, so credentials are passed by interpolating Terraform values
into that string. Define them as `sensitive` variables and build a
`PSCredential` in the script:

```hcl
variable "service_account_username" {
  type = string
}

variable "service_account_password" {
  type      = string
  sensitive = true
}

provider "powershell" {
  startup_script = <<-PS
    $global:ProviderState = @{}
    # Credentials interpolated from Terraform variables
    $user = '${var.service_account_username}'
    $pass = ConvertTo-SecureString '${var.service_account_password}' -AsPlainText -Force
    $global:ProviderState["credential"]  = [System.Management.Automation.PSCredential]::new($user, $pass)
    $global:ProviderState["environment"] = "production"

    # Import required modules and authenticate
    Import-Module Az.Accounts
    Connect-AzAccount -Credential $global:ProviderState["credential"]
  PS
}
```

> **Tip:** the dedicated [connection arguments](#using-connection-arguments)
> (`username`/`password`/…) give you typed alternatives to interpolating
> credentials into the script string. They are exposed to scripts as
> `$global:ProviderData`.

Supply the values without committing them — e.g. via environment variables
(`TF_VAR_service_account_password`) or a CLI prompt:

```bash
export TF_VAR_service_account_username="svc-deploy"
export TF_VAR_service_account_password="…"
terraform apply
```

> **Security:** Interpolated values become part of the rendered `startup_script`.
> Provider configuration is **not** stored in Terraform state, but it *is*
> embedded in saved plan files (`terraform plan -out`), and secrets interpolated
> into **resource** attributes (scripts, `input_data`) *are* stored in state.
> Mark password variables `sensitive`, and prefer `TF_VAR_*` environment
> variables over `*.tfvars` files checked into source control.

To keep a secret out of state entirely, pass it through the provider process
environment instead and have the script read `$env:` directly — the value never
touches the rendered script or state:

```powershell
# scripts/init.ps1 — set SVC_PASSWORD in the shell before `terraform apply`
$global:ProviderState = @{}
$user = $env:SVC_USERNAME
$pass = ConvertTo-SecureString $env:SVC_PASSWORD -AsPlainText -Force
$global:ProviderState["credential"] = [System.Management.Automation.PSCredential]::new($user, $pass)

Import-Module Az.Accounts
Connect-AzAccount -Credential $global:ProviderState["credential"]
```

## Sharing state across multiple resources

`startup_script` and `shutdown_script` run **once per Terraform run**, not once
per resource. Every `powershell_script` resource is processed in the same
persistent PowerShell process, so any global the startup script creates (here
`$global:ProviderState`, a name you pick) is available to all of them — and the
shutdown script sees it too, after the last resource has finished.

A typical pattern: connect once in the startup script, reuse that connection in
each resource, and disconnect once in the shutdown script.

```hcl
provider "powershell" {
  startup_script = <<-PS
    # Runs once, before any resource. Establish the shared connection.
    $global:ProviderState = @{}
    Import-Module Az.Accounts
    Connect-AzAccount -Identity
    $global:ProviderState["subscription"] = (Get-AzContext).Subscription.Id
  PS

  shutdown_script = <<-PS
    # Runs once, after the last resource. Clean up the shared connection.
    Disconnect-AzAccount | Out-Null
  PS
}

resource "powershell_script" "web" {
  create_script = <<-PS
    $sub = $global:ProviderState["subscription"]   # shared from startup_script
    # ... create the "web" resource against $sub ...
    [PSCustomObject]@{ id = "web"; subscription = $sub }
  PS
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")
}

resource "powershell_script" "db" {
  create_script = <<-PS
    $sub = $global:ProviderState["subscription"]   # same shared connection
    # ... create the "db" resource against $sub ...
    [PSCustomObject]@{ id = "db"; subscription = $sub }
  PS
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")
}
```

Here `Connect-AzAccount` runs exactly once regardless of how many
`powershell_script` resources reference the provider, and `Disconnect-AzAccount`
runs exactly once after both `web` and `db` have been processed.

### Isolation guarantees

Sharing the process does **not** mean resource scripts can see each other's
*local* variables. Each CRUD script runs in an isolated child scope: only the
object emitted to the output stream is captured, and any non-global variables a
script creates are discarded when it finishes. State that must survive between
operations has to be stored in a `$global:*` variable (created in
`startup_script`), which lives in the persistent runspace.

There is **no automatic rollback** of globals. If a CRUD script mutates a global
and then fails, the mutation stays in effect; write idempotent scripts, or guard
mutations until after the work that can fail has succeeded.

## Using connection arguments

The provider accepts a set of optional, typed connection arguments — `server`,
`username`, `password` (sensitive), `cert_thumbprint`, `provider_data`, and
`sensitive_provider_data` (JSON object strings). Whatever you set is decoded once
and exposed to **every** script (startup, CRUD, shutdown) as the
`$global:ProviderData` hashtable:

```hcl
provider "powershell" {
  server          = "db01.example.com"
  username        = "svc-deploy"
  password        = var.deploy_password           # sensitive
  cert_thumbprint = "AB12CD34…"
  provider_data   = jsonencode({ environment = "prod", retries = 3 })

  sensitive_provider_data = jsonencode({ api_key = var.api_key })   # redacted from output
}

resource "powershell_script" "thing" {
  create_script = <<-PS
    $server = $global:ProviderData.server                    # "db01.example.com"
    $user   = $global:ProviderData.username                  # "svc-deploy"
    $env    = $global:ProviderData.Data.environment          # "prod"
    # ... use them to create the resource ...
    [PSCustomObject]@{ id = "thing-1"; server = $server }
  PS
  read_script   = "[PSCustomObject]@{ id = 'thing-1' }"
  update_script = "[PSCustomObject]@{ id = 'thing-1' }"
  delete_script = "# ..."
}
```

`$global:ProviderData` is read-only configuration and is separate from any
`$global:*` state your scripts manage. Provider configuration is never stored in
Terraform state (though it is embedded in saved plan files). To keep `password`
and `session_password` out of configuration files entirely, supply them through
the `POWERSHELL_PROVIDER_PASSWORD` / `POWERSHELL_PROVIDER_SESSION_PASSWORD`
environment variables — an explicitly configured attribute wins over the
environment. Other secrets belong in `sensitive_provider_data` (exposed as
`$global:ProviderData.SensitiveData`).

## Execution model and timeouts

All operations of one provider configuration run **serially** through its single
persistent PowerShell process — Terraform's usual parallelism does not apply
within a configuration. This is what makes shared `$global:*` state safe. For
parallel execution against independent targets, use provider aliases: each alias
gets its own sidecar process.

If a script exceeds its timeout, the sidecar process is force-killed because its
runspace state is unrecoverable mid-script. **Every remaining operation in that
Terraform run then fails fast** with an error referencing the timeout. Set
generous `timeout` values (default 3600s) for long-running scripts rather than
relying on recovery after a kill.

Scripts must also never print the literal protocol marker lines
(`###PS_RSP_START###` / `###PS_RSP_END###`) to standard output; they would
corrupt the framing between the provider and its PowerShell host.

## Remote sessions: running scripts on another machine

Set `session_type` to make the host open a remote PowerShell session **before**
the startup script and run **every** script — startup, CRUD, and shutdown — on
the remote computer (modeling `Enter-PSSession`: connect first, then set up the
provider arguments on the remote host). The connection arguments above and your
own `$global:*` state then live in the remote runspace. The session is closed
after `shutdown_script` runs, when the provider tears down.

Three transports are supported, each with the most common authentication
combinations. Prefix every related argument with `session_`.

### WinRM

```hcl
provider "powershell" {
  session_type           = "winrm"
  session_host           = "server01.corp.example.com"
  session_username       = "CORP\\svc-deploy"
  session_password       = var.win_password
  session_use_ssl        = true                # WinRM over HTTPS (5986)
  session_authentication = "Negotiate"         # or Kerberos, Basic, Credssp, ...
  # session_configuration_name = "PowerShell.7"
  # session_cert_thumbprint    = "AB12…"       # certificate auth instead of password
}
```

### SSH

```hcl
provider "powershell" {
  session_type     = "ssh"
  session_host     = "linux01.example.com"
  session_username = "deploy"
  session_key_file = "~/.ssh/id_ed25519"       # key-based auth (recommended for SSH)
  # session_port   = 2222
}
```

### VM guest (PowerShell Direct)

```hcl
provider "powershell" {
  session_type     = "vmguest"
  session_vm_name  = "WIN-BUILD-01"            # or session_vm_id = "<guid>"
  session_username = "Administrator"
  session_password = var.vm_password           # a credential is required
}
```

> **Testing note:** remote sessions require a reachable WinRM/SSH endpoint or a
> local Hyper-V VM, so they cannot be exercised by the project's automated tests
> (which have no remote host). Validate a remote configuration manually with a
> real target — e.g. have a script emit `$env:COMPUTERNAME` (Windows) or run
> `hostname` to confirm it executed on the remote machine.

## Using two provider aliases in a submodule

Each `provider "powershell"` block — the default configuration and every `alias`
— runs in its **own sidecar process with its own runspace and its own globals**
(and its own remote session, if configured). One alias cannot see another's
state. This makes
aliases the right tool when a single module must act against two independent
targets (two tenants, two regions, a primary and a replica), each with its own
`startup_script` connection.

Wiring an aliased configuration into a module takes three pieces:

1. The module declares which alias slots it expects via `configuration_aliases`.
2. Inside the module, each resource selects a slot with `provider = powershell.<alias>`.
3. The caller fills those slots with concrete configurations via a `providers` map.

### The module

The module declares its alias slots in `required_providers`. These are *named
slots the caller must fill* — not provider configs the module defines itself.

```hcl
# modules/replicated-record/main.tf
terraform {
  required_providers {
    powershell = {
      source                = "markdomansky/powershell"
      configuration_aliases = [powershell.primary, powershell.replica]
    }
  }
}

# Writes to the primary target (its own sidecar / connection).
resource "powershell_script" "primary" {
  provider = powershell.primary

  create_script = file("${path.module}/scripts/create.ps1")
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")

  input_data = jsonencode({ name = var.name, value = var.value })
}

# Same scripts, but routed to the replica target's sidecar / connection.
resource "powershell_script" "replica" {
  provider = powershell.replica

  create_script = file("${path.module}/scripts/create.ps1")
  read_script   = file("${path.module}/scripts/read.ps1")
  update_script = file("${path.module}/scripts/update.ps1")
  delete_script = file("${path.module}/scripts/delete.ps1")

  input_data = jsonencode({ name = var.name, value = var.value })
}
```

```hcl
# modules/replicated-record/variables.tf
variable "name"  { type = string }
variable "value" { type = string }
```

```hcl
# modules/replicated-record/outputs.tf
output "primary_region" {
  value = jsondecode(powershell_script.primary.output_data).region
}

output "replica_region" {
  value = jsondecode(powershell_script.replica.output_data).region
}
```

A script reads the per-alias connection details from its own
`$global:ProviderState`, so the same `create.ps1` behaves differently depending
on which alias routed it:

```powershell
# modules/replicated-record/scripts/create.ps1
$region = $global:ProviderState["region"]   # set by THIS alias's startup_script
# ... create $InputData.name in $region ...
[PSCustomObject]@{
  id     = "$region/$($InputData.name)"
  region = $region
  value  = $InputData.value
}
```

### The caller

The root module defines two aliased configurations and passes them into the
module's slots with a `providers` map. The map's left side is the slot name the
module declared; the right side is the concrete configuration to bind to it.

```hcl
# main.tf
terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

provider "powershell" {
  alias = "primary"
  startup_script = <<-PS
    $global:ProviderState = @{}
    Connect-AzAccount -Identity
    $global:ProviderState["region"] = "eastus"
  PS
}

provider "powershell" {
  alias = "replica"
  startup_script = <<-PS
    $global:ProviderState = @{}
    Connect-AzAccount -Identity
    $global:ProviderState["region"] = "westus"
  PS
}

module "config_record" {
  source = "./modules/replicated-record"

  # Bind the module's alias slots to concrete configurations.
  providers = {
    powershell.primary = powershell.primary
    powershell.replica = powershell.replica
  }

  name  = "app-config"
  value = "hello"
}

output "regions" {
  value = [module.config_record.primary_region, module.config_record.replica_region]
}
```

Because the two configurations are separate sidecars, `config_record` connects
to `eastus` and `westus` independently and in isolation — a failure or state
written under `primary` never leaks into `replica`.

> **Names don't have to match.** The slot names inside the module
> (`powershell.primary`, `powershell.replica`) are independent of the caller's
> alias names. If the root called its configurations `powershell.east` and
> `powershell.west`, the map would read
> `powershell.primary = powershell.east` and `powershell.replica = powershell.west`.

### Two resources sharing one aliased configuration

The previous example routed each resource to a *different* alias. The opposite is
just as common: point **two (or more) resources at the same alias** so they share
that one configuration's single `startup_script` and `$global:ProviderState`.
The startup script still runs **once** for the alias — not once per resource — and
both resources reuse the connection it established.

```hcl
# A single aliased configuration: one sidecar, one startup_script.
provider "powershell" {
  alias = "tenant_a"
  startup_script = <<-PS
    # Runs once for this alias, before either resource below.
    $global:ProviderState = @{}
    Connect-AzAccount -Identity
    $global:ProviderState["tenant"]  = "tenant-a"
    $global:ProviderState["session"] = (Get-AzContext).Account.Id
  PS
}

# Both resources select the SAME alias, so both see the same shared state.
resource "powershell_script" "user" {
  provider = powershell.tenant_a

  create_script = <<-PS
    $tenant = $global:ProviderState["tenant"]    # shared from tenant_a startup
    # ... create the user in $tenant ...
    [PSCustomObject]@{ id = "user"; tenant = $tenant }
  PS
  read_script   = "..."
  update_script = "..."
  delete_script = "..."
}

resource "powershell_script" "group" {
  provider = powershell.tenant_a

  create_script = <<-PS
    $tenant = $global:ProviderState["tenant"]    # same connection, same state
    # ... create the group in $tenant ...
    [PSCustomObject]@{ id = "group"; tenant = $tenant }
  PS
  read_script   = "..."
  update_script = "..."
  delete_script = "..."
}
```

This is exactly the [Sharing state across multiple resources](#sharing-state-across-multiple-resources)
behavior, applied to an aliased configuration instead of the default one:
`Connect-AzAccount` runs a single time for `tenant_a`, and both `user` and
`group` run in that alias's sidecar reading the same `$global:ProviderState`.

In a submodule, this means the module only needs **one** alias slot even though
it manages several resources:

```hcl
# modules/tenant-objects/main.tf
terraform {
  required_providers {
    powershell = {
      source                = "markdomansky/powershell"
      configuration_aliases = [powershell.tenant]   # one slot, shared by all resources
    }
  }
}

resource "powershell_script" "user"  {
  provider = powershell.tenant
  # ...
}

resource "powershell_script" "group" {
  provider = powershell.tenant
  # ...
}
```

```hcl
# caller binds the single slot once
module "tenant_a_objects" {
  source    = "./modules/tenant-objects"
  providers = { powershell.tenant = powershell.tenant_a }
}
```
