# Remote session + connection arguments example

Demonstrates the provider's connection arguments and remote PowerShell sessions:

- **Connection arguments** — `server`, `username`, `password` (sensitive),
  `cert_thumbprint`, `provider_data`, and `sensitive_provider_data` (JSON object
  strings) are exposed to every script as the `$global:ProviderData` hashtable.
- **Remote sessions** — setting `session_*` arguments makes the provider open a
  remote session (WinRM, SSH, or VM guest) *before* the startup script and run
  **all** scripts on the remote computer. The session is closed after the
  shutdown script when the provider tears down.

See the provider docs for the full argument reference and the supported
authentication combinations:
[Using connection arguments](../../docs/index.md#using-connection-arguments) and
[Remote sessions](../../docs/index.md#remote-sessions-running-scripts-on-another-machine).

## Running it

This example requires a **reachable remote host**, so it can't run unattended.
Edit `main.tf` to pick one transport (WinRM or SSH), fill in real values, then:

```bash
export TF_VAR_remote_password="…"   # keep secrets out of files
terraform init
terraform apply
```

The `probe` output reports the remote computer name (`ran_on`) and the connection
arguments the remote scripts read from `$global:ProviderData`, confirming the
scripts executed remotely.

> Remote sessions cannot be covered by the project's automated tests (CI has no
> remote host); validate them manually against a real target.
