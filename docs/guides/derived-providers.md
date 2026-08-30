---
page_title: "Building derived providers"
description: |-
  Build your own named, typed Terraform provider on top of this engine -
  resources are folders of PowerShell scripts plus a schema manifest, no Go
  required.
---

# Building derived providers

The generic `powershell_script` resource trades typed plans for flexibility:
`input_data`/`output_data` are opaque JSON strings. When you outgrow that -
you want `exchange_mailbox` with real attributes, per-attribute plan diffs,
validation, and your own registry page - build a **derived provider** from the
template repo:

**<https://github.com/markdomansky/TEMPLATE-terraform-provider-YOURPROVIDER>**

Fork it, run one init script, and add a folder per resource:

```text
provider/resources/mailbox/
├── resource.tfps.json  # typed attributes (required/optional/computed,
│                       # sensitive, requires_replace, defaults, validators)
├── create.ps1          # $InputData in, one object (incl. id) out
├── read.ps1
├── update.ps1          # optional: presence enables in-place updates
└── delete.ps1
```

Manifests are named `resource.tfps.json`, `datasource.tfps.json`,
`provider.tfps.json`, and `settings.tfps.json` so editors can auto-associate the published JSON Schemas from
[SchemaStore](https://www.schemastore.org) by filename. Each should also carry a
`"$schema"` key, which the engine accepts and ignores.

The fork compiles a ~50-line managed `main.go` that embeds your `provider/`
tree and hands it to this repo's public Go package:

```go
import "github.com/markdomansky/terraform-provider-powershell/scriptprovider"

scriptprovider.Serve(ctx, scriptprovider.Definition{
    Settings: settings,   // name + registry address from provider/settings.tfps.json
    Version:  version,
    FS:       providerFS, // go:embed all:provider
})
```

`scriptprovider` parses every manifest into a real
terraform-plugin-framework schema, maps typed config onto the `$InputData`
hashtable your scripts already know, maps the emitted object back onto typed
computed attributes, and reuses this provider's entire engine: the persistent
`pshost` sidecar, remote sessions (`session_*`), startup/shutdown scripts, and
timeouts. The fork's release pipeline downloads the matching sidecar binaries
from this repo's releases at package time - derived providers never compile C#
(or vendor engine code).

Prefer the generic route when a full provider is overkill: the
[submodule pattern](../index.md#using-two-provider-aliases-in-a-submodule)
wraps `powershell_script` in a Terraform module with typed variables and needs
no compilation at all.

## Compatibility notes

- `scriptprovider` is a **public API** consumed by every derived provider.
  Renaming or changing its exported surface (`Serve`, `NewProviderFactory`,
  `Definition`, `Settings`, `LoadSettings`, `Factory`, ...) or the manifest
  format is a breaking change for forks, even though this repo's semver maps
  breaking to minor.
- The engine's release zips are also a distribution channel: derived
  providers extract `pshost` from them by name. Keep the archive layout
  (sidecar at the zip root) stable.
