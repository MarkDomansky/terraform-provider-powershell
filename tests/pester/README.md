# Pester end-to-end tests

These tests exercise the **compiled** provider through **real terraform**. Each
test builds `terraform-provider-powershell` from source, stages the compiled
`pshost` sidecar next to it, and points terraform at that binary with a
`dev_overrides` CLI config. terraform then runs `apply`/`destroy` against real
HCL — so the whole shipped stack (provider binary → sidecar process → PowerShell
runspace) is covered, including provider aliases and terraform's
`provider = powershell.<alias>` routing.

This is the counterpart to the Go unit tests in `scriptprovider/`, which keep
covering the Go-only layers (PSManager protocol, `Configure`, the `Factory`, and
`Close` idempotency) that terraform can't reach.

All tests emit verbose, timestamped progress via `Write-TFLog` (visible under
`Detailed` output and in CI logs), including every terraform command they run.

## What's covered

Always-on (cross-platform, run in CI):

- `Resource.Tests.ps1` — `powershell_script` CRUD: file-backed create/read/destroy,
  JSON input/output, provider startup-script state sharing, multiple resources.
- `Alias.Tests.ps1` — provider aliases run in **separate sidecar processes**
  (distinct `$PID`) and **cannot read each other's** globals (e.g. `$global:Shared`).
- `Chaining.Tests.ps1` — rich nested data round-trips into state and flows from one
  resource's `output_data` into another's `input_data`.
- `Isolation.Tests.ps1` — an incidental variable from one resource's script does not
  leak into the next (CRUD scripts run in an isolated child scope).
- `Ps1File.Tests.ps1` — CRUD scripts loaded from real **`.ps1` files** with `file()`
  (values passed via `input_data`, since Terraform reads `.ps1` files verbatim);
  covers create/read and an in-place update.
- `StateRebuild.Tests.ps1` — a **lost terraform state is rebuilt from the TF file**:
  after the state file is deleted, `terraform import` (carrying only the id) re-adopts
  the same real object via the read script, with no replacement.
- `UpdateSemantics.Tests.ps1` — update-vs-replace rules: input changes run
  `update_script` in place, script edits are config-only updates that execute
  nothing, omitting `update_script` makes input changes force a replacement, and
  malformed `input_data` JSON fails at plan time.
- `SensitiveData.Tests.ps1` — `sensitive_input_data` merges into `$InputData`,
  the reserved `sensitive` output key splits into `sensitive_output_data`,
  `sensitive_provider_data` surfaces as `$global:ProviderData.SensitiveData`, and
  none of it leaks into apply/plan output.
- `DataSource.Tests.ps1` — the `powershell_script` data source: merged input,
  provider-global visibility, the sensitive output split, and empty output
  yielding `"{}"`.

Config-gated integration suites (need REAL remote hosts/credentials; **Skipped** in
CI and until configured — see below):

- `RemoteSession.Tests.ps1` — single- and double-hop remote execution across the
  `session_type`s (`winrm` / `ssh` / `vmguest`). A probe reports the hostname each hop
  lands on; double-hop opens a second `PSSession` from hop1 to hop2.
- `LinuxToWindows.Tests.ps1` — **Linux → Windows** remoting over SSH: runs only on a
  Linux host and proves the provider can open a session into a Windows target (the
  probe reports `Win32NT`). The Windows host must run OpenSSH + the PowerShell
  subsystem; SSH remoting needs a **key file** (`SessionKeyFile`), since it cannot
  take a password non-interactively.
- `Dns.Tests.ps1` — full DNS A-record lifecycle against a live Windows DNS server:
  create → update → rebuild state via `import` → destroy.

## Configuring the remote/DNS integration suites

`RemoteSession.Tests.ps1` and `Dns.Tests.ps1` are parameter-driven from a config
file and **skip cleanly** when it is absent or not filled in:

1. Copy `config/remote.tests.config.template.psd1` to
   `config/remote.tests.config.psd1` (the latter is **gitignored** so real
   credentials stay local).
2. Set `Enabled = $true` on the scenarios you want and replace every `TBD` password
   (or supply an SSH key file). A scenario runs only when it is Enabled **and** its
   password is neither empty nor `TBD`; otherwise it is reported as Skipped.

The shipped `config/remote.tests.config.psd1` is pre-filled with the known hosts
(`webdebug.domain1.local`, `dc25a.domain1.local`, `markd@domain1.local`) but with
`TBD` passwords, so it stays skipped until a real password is supplied.

## Prerequisites

- PowerShell 7+ (`pwsh`) and Pester 5 (`Install-Module Pester -MinimumVersion 5.0`)
- `go`, `terraform`, and the .NET SDK (to build the sidecar) on `PATH`
- A compiled `pshost` sidecar. The suite auto-discovers it under
  `csharp/PSHost/bin/Release/.../pshost.exe`, or set `PSHOST_PATH` to its location:

  ```pwsh
  dotnet publish csharp/PSHost/PSHost.csproj -r win-x64 --self-contained -c Release -o dist
  $env:PSHOST_PATH = "$PWD/dist/pshost.exe"
  ```
- A Windows Domain controller running DNS
- A Windows Domain member
- A Debian Linux
  - All systems need PowerShell 7 and terraform installed

## Running

```pwsh
$cfg = New-PesterConfiguration
$cfg.Run.Path = 'tests/pester'
$cfg.Output.Verbosity = 'Detailed'
Invoke-Pester -Configuration $cfg
```

The suite builds the provider into `tests/pester/.bin/` (gitignored) and runs
each terraform workspace in its own temp directory, destroying it afterward.
