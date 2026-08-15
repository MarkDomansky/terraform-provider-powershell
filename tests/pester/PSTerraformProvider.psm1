# Helper module for the Pester suite. These tests drive REAL terraform against
# the COMPILED provider (terraform-provider-powershell) and the COMPILED pshost
# sidecar, using a dev_overrides CLI config so terraform loads the freshly built
# binary instead of a registry copy. This is the end-to-end counterpart to the
# Go unit tests: it exercises the shipped artifacts and real HCL, including
# provider aliases.

$ErrorActionPreference = 'Stop'

function Write-TFLog {
    # Verbose, always-visible test diagnostics. Pester captures Write-Host, so these
    # lines show up in 'Detailed' output and in CI logs, giving a timestamped trace
    # of every workspace, terraform command, and assertion the suite runs. Use this
    # (not Write-Verbose) so the trace appears without callers having to flip
    # $VerbosePreference. Keep messages short; one line per meaningful step.
    param(
        [Parameter(Mandatory, Position = 0)][string]$Message,
        [Parameter(Position = 1)][ValidateSet('INFO', 'STEP', 'WARN', 'SKIP')][string]$Level = 'INFO'
    )
    $ts = (Get-Date).ToString('HH:mm:ss.fff')
    $color = switch ($Level) {
        'STEP' { 'Cyan' }
        'WARN' { 'Yellow' }
        'SKIP' { 'DarkGray' }
        default { 'DarkCyan' }
    }
    Write-Host "[$ts][pstf][$Level] $Message" -ForegroundColor $color
}

# Repo root is two levels up from tests/pester.
$script:RepoRoot      = (Resolve-Path (Join-Path $PSScriptRoot '..' '..')).Path
$script:ProviderBinDir = $null
$script:Exe = if ($IsWindows) { '.exe' } else { '' }

# Provider source address. Must match providerserver.ServeOpts.Address in main.go
# (registry.terraform.io is the implied host, so the dev_overrides key omits it).
$script:ProviderSource = 'markdomansky/powershell'

function Resolve-Tool {
    # Find an executable on PATH, falling back to a list of known locations.
    param([string]$Name, [string[]]$Fallbacks = @())
    $cmd = Get-Command $Name -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    foreach ($f in $Fallbacks) { if (Test-Path $f) { return (Resolve-Path $f).Path } }
    throw "Could not locate '$Name' on PATH or known fallback locations."
}

function Get-DotnetRid {
    # The .NET runtime identifier for the current OS/arch (e.g. win-x64, linux-x64,
    # osx-arm64), used to locate the RID-specific sidecar build output. Keeps the
    # sidecar lookup working on Windows, Linux, and macOS alike.
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
    if ($IsWindows) { return "win-$arch" }
    elseif ($IsMacOS) { return "osx-$arch" }
    else { return "linux-$arch" }
}

function Resolve-Sidecar {
    # Locate the compiled pshost sidecar: PSHOST_PATH wins, then the published
    # build for this platform's RID, then any pshost under the C# bin output.
    if ($env:PSHOST_PATH -and (Test-Path $env:PSHOST_PATH)) { return (Resolve-Path $env:PSHOST_PATH).Path }

    $name = "pshost$script:Exe"
    $rid = Get-DotnetRid
    $candidates = @(
        "csharp/PSHost/bin/Release/net9.0/$rid/$name",
        "csharp/PSHost/bin/Debug/net9.0/$rid/$name",
        "csharp/PSHost/bin/Release/net9.0/$name",
        "csharp/PSHost/bin/Debug/net9.0/$name"
    ) | ForEach-Object { Join-Path $script:RepoRoot $_ }
    foreach ($c in $candidates) { if (Test-Path $c) { return (Resolve-Path $c).Path } }

    # Last resort: search the bin output, skipping intermediate obj/ dirs. The
    # regex matches either path separator so it works on Windows and POSIX.
    $found = Get-ChildItem -Path (Join-Path $script:RepoRoot 'csharp') -Recurse -Filter $name -ErrorAction SilentlyContinue |
        Where-Object { $_.FullName -notmatch '[\\/]obj[\\/]' } | Select-Object -First 1
    if ($found) { return $found.FullName }

    throw "Could not find the compiled pshost sidecar. Build it (dotnet publish csharp/PSHost) or set PSHOST_PATH."
}

function Initialize-ProviderBin {
    # Build the provider from source and stage it next to the sidecar in a
    # dedicated bin dir, so findSidecarBinary resolves the sidecar by proximity.
    # Idempotent within a session unless -Force is given.
    param([switch]$Force)

    if (-not $Force -and $script:ProviderBinDir -and (Test-Path $script:ProviderBinDir)) {
        return $script:ProviderBinDir
    }

    # Release-artifact mode: PSTF_PROVIDER_BIN_DIR points at a directory that
    # already holds the packaged provider binary and pshost sidecar (an
    # extracted release zip). Use it verbatim - no go build, no staging - so
    # the suite exercises the exact bytes that will be published.
    if ($env:PSTF_PROVIDER_BIN_DIR) {
        if (-not (Test-Path $env:PSTF_PROVIDER_BIN_DIR)) {
            throw "PSTF_PROVIDER_BIN_DIR is set but does not exist: $($env:PSTF_PROVIDER_BIN_DIR)"
        }
        $dir = (Resolve-Path $env:PSTF_PROVIDER_BIN_DIR).Path
        if (-not (Get-ChildItem $dir -Filter "terraform-provider-powershell*")) {
            throw "PSTF_PROVIDER_BIN_DIR '$dir' contains no terraform-provider-powershell binary."
        }
        if (-not (Test-Path (Join-Path $dir "pshost$script:Exe"))) {
            throw "PSTF_PROVIDER_BIN_DIR '$dir' contains no pshost$script:Exe sidecar."
        }
        $script:ProviderBinDir = $dir
        return $script:ProviderBinDir
    }

    $go = Resolve-Tool -Name 'go' -Fallbacks @('C:\Program Files\Go\bin\go.exe')
    $binDir = Join-Path $PSScriptRoot '.bin'
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null

    $providerExe = Join-Path $binDir "terraform-provider-powershell$script:Exe"
    Push-Location $script:RepoRoot
    try {
        & $go build -o $providerExe .
        if ($LASTEXITCODE -ne 0) { throw "go build failed with exit code $LASTEXITCODE" }
    } finally {
        Pop-Location
    }

    # Stage the sidecar next to the provider so it is discovered by proximity.
    # The build-output sidecar is RID-specific and needs its sibling runtime
    # DLLs/runtimeconfig, so copy the whole directory, not just the exe.
    #
    # When the whole suite runs, each test file re-imports this module and re-stages
    # into the shared .bin dir. A sidecar process spawned by a previous test file can
    # still hold a DLL here for a moment after its terraform destroy, locking the
    # copy. Retry briefly; if a file stays locked but the sidecar is already staged,
    # that copy is byte-identical and safe to keep.
    $sidecar = Resolve-Sidecar
    $sidecarDir = Split-Path -Parent $sidecar
    $sidecarExe = Join-Path $binDir "pshost$script:Exe"
    $staged = $false
    for ($i = 0; $i -lt 5 -and -not $staged; $i++) {
        try {
            Copy-Item -Path (Join-Path $sidecarDir '*') -Destination $binDir -Recurse -Force
            $staged = $true
        } catch {
            if (Test-Path $sidecarExe) { $staged = $true } else { Start-Sleep -Milliseconds 200 }
        }
    }
    if (-not $staged) { throw "Failed to stage the pshost sidecar into '$binDir' (files locked)." }

    $script:ProviderBinDir = (Resolve-Path $binDir).Path
    return $script:ProviderBinDir
}

function New-TFWorkspace {
    # Create an isolated terraform workspace: a temp dir holding the terraform
    # config plus a dev_overrides CLI config that points terraform at the
    # compiled provider.
    #
    # The config can be supplied two ways:
    #   -Config     an inline HCL string written to main.tf, or
    #   -ConfigPath a directory of real .tf files (copied in verbatim) so the
    #               same configs can be parsed/validated by terraform tooling.
    # -Variables writes a terraform.tfvars so runtime values (e.g. temp paths)
    # can stay out of the .tf files.
    [CmdletBinding(DefaultParameterSetName = 'Inline')]
    param(
        [Parameter(Mandatory, ParameterSetName = 'Inline')][string]$Config,
        [Parameter(Mandatory, ParameterSetName = 'Path')][string]$ConfigPath,
        [hashtable]$Variables
    )

    if (-not $script:ProviderBinDir) { throw "Call Initialize-ProviderBin before New-TFWorkspace." }

    $dir = Join-Path ([System.IO.Path]::GetTempPath()) ("pstf-" + [System.IO.Path]::GetRandomFileName())
    New-Item -ItemType Directory -Force -Path $dir | Out-Null

    if ($PSCmdlet.ParameterSetName -eq 'Path') {
        $resolved = Resolve-Path $ConfigPath
        # Copy the whole config tree, not just *.tf, so file()-loaded scripts under
        # scripts/ (and any other supporting files) come along into the workspace.
        # Source config dirs are clean (no state/.terraform), so a plain recursive
        # copy is safe.
        Copy-Item -Path (Join-Path $resolved '*') -Destination $dir -Recurse
        Write-TFLog "workspace <- $resolved (recursive copy)"
    } else {
        Set-Content -Path (Join-Path $dir 'main.tf') -Value $Config -Encoding UTF8
    }

    if ($Variables) {
        # Values are written as quoted HCL strings; backslashes are forward-slashed
        # so Windows paths stay valid in HCL.
        $lines = foreach ($name in $Variables.Keys) {
            $value = ([string]$Variables[$name]) -replace '\\', '/'
            "$name = `"$value`""
        }
        Set-Content -Path (Join-Path $dir 'terraform.tfvars') -Value ($lines -join "`n") -Encoding UTF8
    }

    # dev_overrides bypasses 'terraform init' and loads the local binary. Forward
    # slashes keep the HCL string valid on Windows.
    $binDirHcl = $script:ProviderBinDir -replace '\\', '/'
    $cliConfig = @"
provider_installation {
  dev_overrides {
    "$script:ProviderSource" = "$binDirHcl"
  }
  direct {}
}
"@
    $cliConfigPath = Join-Path $dir 'dev.tfrc'
    Set-Content -Path $cliConfigPath -Value $cliConfig -Encoding UTF8

    Write-TFLog "workspace ready at $dir"
    return [pscustomobject]@{
        Dir           = $dir
        CliConfigPath = $cliConfigPath
        SidecarPath   = (Join-Path $script:ProviderBinDir "pshost$script:Exe")
    }
}

function Invoke-TF {
    # Run a terraform subcommand inside a workspace with the dev_overrides config
    # and sidecar path wired in. Throws on non-zero exit, surfacing the output.
    param(
        [Parameter(Mandatory)][pscustomobject]$Workspace,
        [Parameter(Mandatory)][string[]]$Arguments
    )
    $terraform = Resolve-Tool -Name 'terraform'

    $prevCliConfig = $env:TF_CLI_CONFIG_FILE
    $prevPsHost    = $env:PSHOST_PATH
    $env:TF_CLI_CONFIG_FILE = $Workspace.CliConfigPath
    $env:PSHOST_PATH        = $Workspace.SidecarPath  # belt-and-suspenders; staging also handles it

    Write-TFLog "terraform $($Arguments -join ' ')" 'STEP'
    Push-Location $Workspace.Dir
    try {
        $output = & $terraform @Arguments 2>&1
        if ($LASTEXITCODE -ne 0) {
            Write-TFLog "terraform $($Arguments[0]) FAILED (exit $LASTEXITCODE)" 'WARN'
            throw "terraform $($Arguments -join ' ') failed (exit $LASTEXITCODE):`n$($output -join "`n")"
        }
        Write-TFLog "terraform $($Arguments[0]) ok"
        return $output
    } finally {
        Pop-Location
        $env:TF_CLI_CONFIG_FILE = $prevCliConfig
        $env:PSHOST_PATH        = $prevPsHost
    }
}

function Get-TFOutput {
    # Return parsed `terraform output -json`. stdout carries clean JSON; the
    # dev_overrides warning goes to stderr and is not captured here.
    param([Parameter(Mandatory)][pscustomobject]$Workspace)
    $terraform = Resolve-Tool -Name 'terraform'

    $prevCliConfig = $env:TF_CLI_CONFIG_FILE
    $env:TF_CLI_CONFIG_FILE = $Workspace.CliConfigPath
    Push-Location $Workspace.Dir
    try {
        $json = & $terraform output -json
        if ($LASTEXITCODE -ne 0) { throw "terraform output failed (exit $LASTEXITCODE)" }
        return ($json | ConvertFrom-Json)
    } finally {
        Pop-Location
        $env:TF_CLI_CONFIG_FILE = $prevCliConfig
    }
}

function Invoke-TFExit {
    # Like Invoke-TF but returns terraform's exit code instead of throwing. Needed
    # for commands whose non-zero exit is meaningful rather than fatal, e.g.
    # `plan -detailed-exitcode` (0 = no changes, 2 = changes present).
    param(
        [Parameter(Mandatory)][pscustomobject]$Workspace,
        [Parameter(Mandatory)][string[]]$Arguments
    )
    $terraform = Resolve-Tool -Name 'terraform'

    $prevCliConfig = $env:TF_CLI_CONFIG_FILE
    $prevPsHost    = $env:PSHOST_PATH
    $env:TF_CLI_CONFIG_FILE = $Workspace.CliConfigPath
    $env:PSHOST_PATH        = $Workspace.SidecarPath

    Write-TFLog "terraform $($Arguments -join ' ') (capturing exit code)" 'STEP'
    Push-Location $Workspace.Dir
    try {
        $output = & $terraform @Arguments 2>&1
        $code = $LASTEXITCODE
        Write-TFLog "terraform $($Arguments[0]) -> exit $code"
        return [pscustomobject]@{ ExitCode = $code; Output = $output }
    } finally {
        Pop-Location
        $env:TF_CLI_CONFIG_FILE = $prevCliConfig
        $env:PSHOST_PATH        = $prevPsHost
    }
}

function Remove-TFWorkspace {
    # terraform destroy (best effort) and delete the temp workspace.
    param([Parameter(Mandatory)][pscustomobject]$Workspace)
    Write-TFLog "tearing down workspace $($Workspace.Dir)"
    try { Invoke-TF -Workspace $Workspace -Arguments @('destroy', '-auto-approve', '-no-color') | Out-Null } catch { }
    Remove-Item -Recurse -Force $Workspace.Dir -ErrorAction SilentlyContinue
}

function Get-RemoteTestConfig {
    # Load the gitignored remote-integration config (real hosts/credentials) if the
    # operator has created one. Returns the parsed hashtable, or $null when absent so
    # the remote/DNS suites can skip cleanly in CI. The committed *.template.psd1 is a
    # blueprint only and is never loaded as the live config.
    $path = Join-Path $PSScriptRoot 'config/remote.tests.config.psd1'
    if (-not (Test-Path $path)) {
        Write-TFLog "no remote.tests.config.psd1 found - remote/DNS suites will skip" 'SKIP'
        return $null
    }
    Write-TFLog "loaded remote config $path"
    return Import-PowerShellDataFile -Path $path
}

function ConvertTo-SessionTfvars {
    # Translate a remote-config scenario hashtable into the string tfvars consumed by
    # the tf/remote-session config. Only non-empty values are emitted; booleans become
    # "true"/"false" so the HCL can tobool() them. session_type is always present.
    # Shared by RemoteSession.Tests.ps1 and LinuxToWindows.Tests.ps1.
    param([Parameter(Mandatory)][hashtable]$Scenario)
    $map = [ordered]@{
        session_type               = $Scenario.SessionType
        session_host               = $Scenario.SessionHost
        session_port               = $Scenario.SessionPort
        session_username           = $Scenario.SessionUsername
        session_password           = $Scenario.SessionPassword
        session_authentication     = $Scenario.SessionAuthentication
        session_cert_thumbprint    = $Scenario.SessionCertThumbprint
        session_configuration_name = $Scenario.SessionConfigurationName
        session_key_file           = $Scenario.SessionKeyFile
        session_vm_name            = $Scenario.SessionVmName
        session_vm_id              = $Scenario.SessionVmId
        hop2_type                  = $Scenario.Hop2Type
        hop2_host                  = $Scenario.Hop2Host
        hop2_username              = $Scenario.Hop2Username
        hop2_password              = $Scenario.Hop2Password
        hop2_key_file              = $Scenario.Hop2KeyFile
        hop2_port                  = $Scenario.Hop2Port
    }
    $vars = @{}
    foreach ($k in $map.Keys) {
        $v = $map[$k]
        if ($null -ne $v -and "$v" -ne '') { $vars[$k] = "$v" }
    }
    if ($Scenario.ContainsKey('SessionUseSSL')) {
        $vars['session_use_ssl'] = if ($Scenario.SessionUseSSL) { 'true' } else { 'false' }
    }
    return $vars
}

function Test-RemoteScenarioReady {
    # A scenario is runnable only when it is explicitly Enabled and every credential
    # placeholder has been filled in. Passwords default to 'TBD' in the shipped config,
    # so any 'TBD'/empty secret means "not configured yet" and the test must skip.
    param([hashtable]$Scenario)
    if (-not $Scenario) { return $false }
    if (-not $Scenario.Enabled) { return $false }
    foreach ($key in 'SessionPassword', 'Hop2Password') {
        if ($Scenario.ContainsKey($key)) {
            $v = [string]$Scenario[$key]
            if ($v -and $v -ne 'TBD') { continue }
            # Allow key-file based auth (no password) when a key file is supplied.
            if ($key -eq 'SessionPassword' -and $Scenario['SessionKeyFile']) { continue }
            if ($key -eq 'Hop2Password' -and $Scenario['Hop2KeyFile']) { continue }
            return $false
        }
    }
    return $true
}

Export-ModuleMember -Function Initialize-ProviderBin, New-TFWorkspace, Invoke-TF, Invoke-TFExit, `
    Get-TFOutput, Remove-TFWorkspace, Write-TFLog, Get-RemoteTestConfig, Test-RemoteScenarioReady, `
    ConvertTo-SessionTfvars
