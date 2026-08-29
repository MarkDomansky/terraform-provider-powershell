package scriptprovider

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Environment variables consulted when the corresponding provider attribute is
// not set in configuration, so secrets can stay out of .tf/.tfvars files.
const (
	envPassword        = "POWERSHELL_PROVIDER_PASSWORD"
	envSessionPassword = "POWERSHELL_PROVIDER_SESSION_PASSWORD"
)

// Compile-time assertions that PowerShellProvider satisfies the framework's
// provider interfaces. If a required method is missing or has the wrong
// signature, the build fails here rather than at registration time.
var (
	_ provider.Provider                   = &PowerShellProvider{}
	_ provider.ProviderWithValidateConfig = &PowerShellProvider{}
)

// PowerShellProvider implements the Terraform provider for PowerShell script
// execution. A single instance owns one persistent PowerShell process (via
// psManager) that lives for the whole Terraform run, so every resource operation
// shares the same interpreter and global state.
type PowerShellProvider struct {
	version   string     // provider version string, surfaced via Metadata
	psManager *PSManager // the persistent PowerShell process; nil until Configure runs
}

// PowerShellProviderModel maps the provider block's HCL attributes onto Go
// fields. The `tfsdk` tags must match the attribute names declared in Schema;
// the framework uses them to decode the user's configuration into this struct.
type PowerShellProviderModel struct {
	StartupScript  types.String `tfsdk:"startup_script"`
	ShutdownScript types.String `tfsdk:"shutdown_script"`
	Timeout        types.Int64  `tfsdk:"timeout"`

	// Connection arguments. All optional; surfaced to every script as the
	// $global:ProviderData hashtable.
	Server                types.String `tfsdk:"server"`
	Username              types.String `tfsdk:"username"`
	Password              types.String `tfsdk:"password"`
	CertThumbprint        types.String `tfsdk:"cert_thumbprint"`
	ProviderData          types.String `tfsdk:"provider_data"`
	SensitiveProviderData types.String `tfsdk:"sensitive_provider_data"`

	// Remote session arguments (session_*). When session_type is set the host
	// connects to the remote computer first and runs every script there.
	SessionType              types.String `tfsdk:"session_type"`
	SessionHost              types.String `tfsdk:"session_host"`
	SessionPort              types.Int64  `tfsdk:"session_port"`
	SessionUsername          types.String `tfsdk:"session_username"`
	SessionPassword          types.String `tfsdk:"session_password"`
	SessionUseSSL            types.Bool   `tfsdk:"session_use_ssl"`
	SessionAuthentication    types.String `tfsdk:"session_authentication"`
	SessionCertThumbprint    types.String `tfsdk:"session_cert_thumbprint"`
	SessionConfigurationName types.String `tfsdk:"session_configuration_name"`
	SessionKeyFile           types.String `tfsdk:"session_key_file"`
	SessionVMName            types.String `tfsdk:"session_vm_name"`
	SessionVMId              types.String `tfsdk:"session_vm_id"`
}

// New returns a factory function for creating the provider.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &PowerShellProvider{
			version: version,
		}
	}
}

// ShutdownProvider is a provider whose persistent PowerShell process (and
// registered shutdown scripts) can be torn down after serving stops. Both the
// generic PowerShellProvider and definition-based providers implement it.
type ShutdownProvider interface {
	provider.Provider
	Shutdown(context.Context) error
}

// Factory creates provider instances while tracking them, so their persistent
// PowerShell processes (and shutdown scripts) can be cleanly torn down when the
// provider server stops. The terraform-plugin-framework offers no provider-level
// teardown callback, so the serving entrypoint must call Shutdown itself.
type Factory struct {
	newProvider func() ShutdownProvider
	mu          sync.Mutex
	instances   []ShutdownProvider
}

// NewFactory returns a Factory that builds generic powershell providers
// reporting the given version.
func NewFactory(version string) *Factory {
	return NewFactoryWith(func() ShutdownProvider {
		return &PowerShellProvider{version: version}
	})
}

// NewFactoryWith returns a Factory that builds and tracks instances produced by
// the given constructor. Used by definition-based (derived) providers, which
// construct a different provider type but need the same teardown tracking.
func NewFactoryWith(newProvider func() ShutdownProvider) *Factory {
	return &Factory{newProvider: newProvider}
}

// New returns a provider factory function suitable for providerserver.Serve.
// Every instance it creates is tracked for later Shutdown.
func (f *Factory) New() func() provider.Provider {
	return func() provider.Provider {
		p := f.newProvider()
		f.mu.Lock()
		f.instances = append(f.instances, p)
		f.mu.Unlock()
		return p
	}
}

// Shutdown tears down every provider instance the factory created, running each
// one's shutdown script exactly once.
func (f *Factory) Shutdown(ctx context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.instances {
		_ = p.Shutdown(ctx)
	}
}

// Metadata reports the provider's type name and version to Terraform. The type
// name ("powershell") becomes the prefix for every resource this provider
// exposes, e.g. the script resource is addressed as "powershell_script".
func (p *PowerShellProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "powershell"
	resp.Version = p.version
}

// Schema declares the attributes accepted in the provider block. Terraform uses
// this both to validate user configuration and to render documentation.
func (p *PowerShellProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The PowerShell provider executes PowerShell scriptblocks for Terraform resource CRUD operations. " +
			"It maintains a persistent PowerShell process for the lifetime of the Terraform run, " +
			"enabling provider-level state sharing across all resource operations.",
		Attributes: builtinProviderAttributes(),
	}
}

// builtinProviderAttributes returns the provider-block attributes shared by the
// generic powershell provider and every definition-based (derived) provider.
// Derived providers merge their custom attributes on top of this map. The map is
// rebuilt on every call so callers may mutate their copy safely.
func builtinProviderAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"startup_script": schema.StringAttribute{
			Description: "PowerShell script to execute during provider initialization (after any remote " +
				"session is opened). Use this to set up provider-level state in your own globals " +
				"(e.g. $global:ProviderState = @{}), authenticate, or load modules. Globals persist " +
				"across all resource operations because the runspace is persistent.",
			Optional: true,
		},
		"shutdown_script": schema.StringAttribute{
			Description: "PowerShell script to execute once when the provider tears down, " +
				"after all resource operations have completed. It runs in the same persistent " +
				"process (and remote session, if any) as the startup script and every resource, so it " +
				"can read the globals it created to clean up (e.g. sign out, release leases, flush buffers).",
			Optional: true,
		},
		"timeout": schema.Int64Attribute{
			Description: "Default timeout in seconds for script execution. " +
				"Individual resources can override this. Defaults to 3600 (1 hour).",
			Optional: true,
			Validators: []validator.Int64{
				int64validator.AtLeast(1),
			},
		},

		// --- Connection arguments -------------------------------------------------
		// All optional. Whatever is set is exposed to every script (startup,
		// CRUD, shutdown) as the $global:ProviderData hashtable, e.g.
		// $global:ProviderData.server / .username / .Data.foo
		"server": schema.StringAttribute{
			Description: "Target server made available to scripts as $global:ProviderData.server.",
			Optional:    true,
		},
		"username": schema.StringAttribute{
			Description: "Username made available to scripts as $global:ProviderData.username.",
			Optional:    true,
		},
		"password": schema.StringAttribute{
			Description: "Password made available to scripts as $global:ProviderData.password. " +
				"Marked sensitive so it is redacted from CLI output. Provider configuration is never " +
				"stored in Terraform state, but it is embedded in saved plan files (terraform plan -out). " +
				"Can also be supplied via the " + envPassword + " environment variable.",
			Optional:  true,
			Sensitive: true,
		},
		"cert_thumbprint": schema.StringAttribute{
			Description: "Certificate thumbprint made available to scripts as $global:ProviderData.cert_thumbprint.",
			Optional:    true,
		},
		"provider_data": schema.StringAttribute{
			Description: "Arbitrary provider data as a JSON object string (use jsonencode({...})). " +
				"Decoded to a hashtable and exposed to scripts as $global:ProviderData.Data.",
			Optional: true,
			Validators: []validator.String{
				jsonObjectValidator{},
			},
		},
		"sensitive_provider_data": schema.StringAttribute{
			Description: "Arbitrary sensitive provider data as a JSON object string (use jsonencode({...})). " +
				"Decoded to a hashtable and exposed to scripts as $global:ProviderData.SensitiveData. " +
				"Marked sensitive so it is redacted from CLI output.",
			Optional:  true,
			Sensitive: true,
			Validators: []validator.String{
				jsonObjectValidator{},
			},
		},

		// --- Remote session arguments (session_*) ---------------------------------
		// When session_type is set, the host opens a remote PowerShell session
		// (New-PSSession) before the startup script and runs EVERY script in that
		// remote runspace. The session is closed after shutdown_script.
		"session_type": schema.StringAttribute{
			Description: "Remote session transport: \"winrm\", \"ssh\", or \"vmguest\". " +
				"Leave unset to run scripts locally.",
			Optional: true,
			Validators: []validator.String{
				stringvalidator.OneOf("winrm", "ssh", "vmguest"),
			},
		},
		"session_host": schema.StringAttribute{
			Description: "Remote computer name or hostname to connect to (winrm and ssh).",
			Optional:    true,
		},
		"session_port": schema.Int64Attribute{
			Description: "Optional port override for the remote connection.",
			Optional:    true,
		},
		"session_username": schema.StringAttribute{
			Description: "Username for the remote session credential.",
			Optional:    true,
		},
		"session_password": schema.StringAttribute{
			Description: "Password for the remote session credential. Marked sensitive. " +
				"Can also be supplied via the " + envSessionPassword + " environment variable.",
			Optional:  true,
			Sensitive: true,
		},
		"session_use_ssl": schema.BoolAttribute{
			Description: "winrm only: connect over HTTPS (WinRM port 5986).",
			Optional:    true,
		},
		"session_authentication": schema.StringAttribute{
			Description: "winrm only: authentication mechanism " +
				"(Default, Basic, Negotiate, Kerberos, Credssp, Digest, NegotiateWithImplicitCredential).",
			Optional: true,
		},
		"session_cert_thumbprint": schema.StringAttribute{
			Description: "winrm only: client-certificate thumbprint for certificate authentication " +
				"(used instead of username/password).",
			Optional: true,
		},
		"session_configuration_name": schema.StringAttribute{
			Description: "winrm only: the session configuration (endpoint) to connect to, e.g. \"PowerShell.7\".",
			Optional:    true,
		},
		"session_key_file": schema.StringAttribute{
			Description: "ssh only: path to the private key file used for authentication.",
			Optional:    true,
		},
		"session_vm_name": schema.StringAttribute{
			Description: "vmguest only: the VM name to connect to via PowerShell Direct (requires session_username/session_password).",
			Optional:    true,
		},
		"session_vm_id": schema.StringAttribute{
			Description: "vmguest only: the VM GUID to connect to via PowerShell Direct (alternative to session_vm_name).",
			Optional:    true,
		},
	}
}

// Configure is called once, before any resource operation. It decodes the
// provider configuration, starts the persistent PowerShell process, runs the
// optional startup script, and registers the optional shutdown script. The
// resulting PSManager is handed to every resource via resp.ResourceData.
func (p *PowerShellProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	// Decode the HCL provider block into our typed model.
	var config PowerShellProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	psManager := configureEngine(ctx, config, nil, "", "", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	p.psManager = psManager

	// Hand a reference to this configured provider to every resource and data
	// source. Their Configure methods receive it as req.ProviderData, which is
	// how they reach the shared psManager.
	resp.DataSourceData = p
	resp.ResourceData = p
}

// configureEngine performs the engine bootstrap shared by the generic powershell
// provider and definition-based (derived) providers: environment fallbacks for
// secrets, timeout defaulting, sidecar launch, the one-time configure command
// (provider data + optional remote session), startup script(s), and shutdown
// script registration. It returns the ready PSManager, or nil after appending a
// diagnostic on failure.
//
// extraProviderData (may be nil) is merged into $global:ProviderData under the
// reserved "Config" key — the channel for a derived provider's custom typed
// provider attributes. definitionStartupScript runs before the practitioner's
// startup_script (a derived provider connects first, then the practitioner
// customizes); definitionShutdownScript is registered after the practitioner's
// shutdown_script, so teardown mirrors startup in reverse.
func configureEngine(ctx context.Context, config PowerShellProviderModel,
	extraProviderData map[string]interface{},
	definitionStartupScript, definitionShutdownScript string,
	diags *diag.Diagnostics) *PSManager {
	// Fall back to environment variables for the secrets, so they can stay out
	// of .tf/.tfvars files entirely. Explicit configuration wins over env.
	if config.Password.IsNull() {
		if v := os.Getenv(envPassword); v != "" {
			config.Password = types.StringValue(v)
		}
	}
	if config.SessionPassword.IsNull() {
		if v := os.Getenv(envSessionPassword); v != "" {
			config.SessionPassword = types.StringValue(v)
		}
	}

	// Default to a 1-hour timeout; override only when the user set one explicitly.
	// (Unknown values can occur during planning when the timeout is computed.)
	timeout := defaultTimeoutSeconds * time.Second
	if !config.Timeout.IsNull() && !config.Timeout.IsUnknown() {
		timeout = time.Duration(config.Timeout.ValueInt64()) * time.Second
	}

	tflog.Info(ctx, "Initializing PowerShell process", map[string]interface{}{
		"timeout": timeout.String(),
	})

	psManager, err := NewPSManager(ctx, timeout)
	if err != nil {
		diags.AddError(
			"Failed to initialize PowerShell",
			fmt.Sprintf("Could not start pshost sidecar process: %s\n\nEnsure the pshost sidecar binary is present alongside the provider executable or on PATH.", err),
		)
		return nil
	}

	// Always send the one-time configure command before the startup script. It seeds
	// $global:ProviderData with the connection arguments and, when session details are
	// present, opens the remote session so the startup script (and every resource) runs
	// against the remote computer.
	providerData, dataErr := buildProviderData(config)
	if dataErr != nil {
		_ = psManager.Close()
		diags.AddError(
			"Invalid provider data",
			dataErr.Error(),
		)
		return nil
	}
	// A derived provider's custom typed attributes ride under the reserved
	// "Config" key, so they can never collide with the practitioner-supplied
	// provider_data ("Data") and sensitive_provider_data ("SensitiveData").
	if len(extraProviderData) > 0 {
		providerData["Config"] = extraProviderData
	}
	if err := psManager.Configure(providerData, buildSessionConfig(config), timeout); err != nil {
		_ = psManager.Close()
		diags.AddError(
			"Provider configuration failed",
			fmt.Sprintf("Failed to configure the PowerShell host (provider data / remote session): %s", err),
		)
		return nil
	}

	// A derived provider's embedded startup.ps1 runs first (typically connect /
	// authenticate using $global:ProviderData.Config), so the practitioner's
	// startup_script below runs against an already-initialized provider.
	if definitionStartupScript != "" {
		tflog.Info(ctx, "Executing provider definition startup script")
		result, err := psManager.Execute("startup", definitionStartupScript, nil, timeout)
		if err != nil {
			_ = psManager.Close()
			diags.AddError(
				"Provider startup failed",
				fmt.Sprintf("Failed to execute the provider's built-in startup script: %s", err),
			)
			return nil
		}
		if !result.Success {
			_ = psManager.Close()
			diags.AddError(
				"Provider startup failed",
				fmt.Sprintf("The provider's built-in startup script returned an error: %s", result.Error),
			)
			return nil
		}
	}

	// Run the startup script if provided
	if !config.StartupScript.IsNull() && !config.StartupScript.IsUnknown() {
		startupScript := config.StartupScript.ValueString()
		if startupScript != "" {
			tflog.Info(ctx, "Executing provider startup script")
			result, err := psManager.Execute("startup", startupScript, nil, timeout)
			if err != nil {
				_ = psManager.Close()
				diags.AddError(
					"Startup script failed",
					fmt.Sprintf("Failed to execute provider startup script: %s", err),
				)
				return nil
			}
			if !result.Success {
				_ = psManager.Close()
				diags.AddError(
					"Startup script failed",
					fmt.Sprintf("Provider startup script returned an error: %s", result.Error),
				)
				return nil
			}
			tflog.Info(ctx, "Provider startup script completed successfully")
		}
	}

	// Register the shutdown scripts so they run once when the provider tears
	// down. They are executed by Shutdown/Close, not here, so they see state
	// accumulated by every resource that ran in between. Order mirrors startup
	// in reverse: the practitioner's shutdown_script first, then the derived
	// provider's shutdown.ps1 (typically disconnect).
	if !config.ShutdownScript.IsNull() && !config.ShutdownScript.IsUnknown() {
		if shutdownScript := config.ShutdownScript.ValueString(); shutdownScript != "" {
			psManager.SetShutdownScript(shutdownScript, timeout)
			tflog.Info(ctx, "Registered provider shutdown script")
		}
	}
	if definitionShutdownScript != "" {
		psManager.SetShutdownScript(definitionShutdownScript, timeout)
		tflog.Info(ctx, "Registered provider definition shutdown script")
	}

	return psManager
}

// strOrEmpty returns the string value of a types.String, or "" when it is null or
// unknown. Used to flatten optional provider arguments into wire values.
func strOrEmpty(s types.String) string {
	if s.IsNull() || s.IsUnknown() {
		return ""
	}
	return s.ValueString()
}

// buildProviderData assembles the $global:ProviderData payload from the connection
// arguments. Only set values are included. The provider_data argument is a JSON
// object string that is decoded into a nested "Data" key (so scripts read it as
// $global:ProviderData.Data.<key>); sensitive_provider_data likewise decodes into
// "SensitiveData". Returns an error only when one of those is set but is not valid
// JSON for an object.
func buildProviderData(config PowerShellProviderModel) (map[string]interface{}, error) {
	data := make(map[string]interface{})
	if v := strOrEmpty(config.Server); v != "" {
		data["server"] = v
	}
	if v := strOrEmpty(config.Username); v != "" {
		data["username"] = v
	}
	if v := strOrEmpty(config.Password); v != "" {
		data["password"] = v
	}
	if v := strOrEmpty(config.CertThumbprint); v != "" {
		data["cert_thumbprint"] = v
	}
	if nested, err := parseJSONObject(config.ProviderData, "provider_data"); err != nil {
		return nil, err
	} else if nested != nil {
		data["Data"] = nested
	}
	if nested, err := parseJSONObject(config.SensitiveProviderData, "sensitive_provider_data"); err != nil {
		return nil, err
	} else if nested != nil {
		data["SensitiveData"] = nested
	}
	return data, nil
}

// isSet reports whether an optional string attribute carries a usable value.
// Unknown values (possible during validation when the value is computed) are
// treated as unset so validation stays permissive until the value is known.
func isSet(s types.String) bool {
	return !s.IsNull() && !s.IsUnknown() && s.ValueString() != ""
}

// ValidateConfig enforces the cross-attribute rules of the session_* arguments
// at plan time, so misconfigurations fail fast with a pointed message instead of
// surfacing as a New-PSSession error from inside the PowerShell host.
func (p *PowerShellProvider) ValidateConfig(ctx context.Context, req provider.ValidateConfigRequest, resp *provider.ValidateConfigResponse) {
	var config PowerShellProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	validateSessionConfig(config, &resp.Diagnostics)
}

// validateSessionConfig holds the cross-attribute session_* rules shared by the
// generic provider's ValidateConfig and definition-based (derived) providers.
func validateSessionConfig(config PowerShellProviderModel, diags *diag.Diagnostics) {
	sessionType := ""
	if !config.SessionType.IsUnknown() && !config.SessionType.IsNull() {
		sessionType = config.SessionType.ValueString()
	} else if config.SessionType.IsUnknown() {
		// The transport is not known yet; the rules below cannot be checked.
		return
	}

	// Which session_* arguments are set, for the "requires session_type" check
	// and the per-transport applicability rules.
	winrmOnly := map[string]bool{
		"session_use_ssl":            !config.SessionUseSSL.IsNull() && !config.SessionUseSSL.IsUnknown(),
		"session_authentication":     isSet(config.SessionAuthentication),
		"session_cert_thumbprint":    isSet(config.SessionCertThumbprint),
		"session_configuration_name": isSet(config.SessionConfigurationName),
	}
	sshOnly := map[string]bool{
		"session_key_file": isSet(config.SessionKeyFile),
	}
	vmguestOnly := map[string]bool{
		"session_vm_name": isSet(config.SessionVMName),
		"session_vm_id":   isSet(config.SessionVMId),
	}
	hostLike := map[string]bool{
		"session_host": isSet(config.SessionHost),
		"session_port": !config.SessionPort.IsNull() && !config.SessionPort.IsUnknown(),
	}

	if sessionType == "" {
		for _, group := range []map[string]bool{winrmOnly, sshOnly, vmguestOnly, hostLike} {
			for name, set := range group {
				if set {
					diags.AddAttributeError(
						path.Root(name),
						"Session Argument Without session_type",
						fmt.Sprintf("%s is set but session_type is not. Set session_type to \"winrm\", \"ssh\", or \"vmguest\" to open a remote session, or remove the session_* arguments to run scripts locally.", name),
					)
				}
			}
		}
		return
	}

	requireUnset := func(group map[string]bool, allowedType string) {
		for name, set := range group {
			if set {
				diags.AddAttributeError(
					path.Root(name),
					"Session Argument Not Applicable",
					fmt.Sprintf("%s only applies when session_type is %q, but session_type is %q.", name, allowedType, sessionType),
				)
			}
		}
	}

	switch sessionType {
	case "winrm", "ssh":
		if !isSet(config.SessionHost) && !config.SessionHost.IsUnknown() {
			diags.AddAttributeError(
				path.Root("session_host"),
				"Missing Remote Host",
				fmt.Sprintf("session_host is required when session_type is %q.", sessionType),
			)
		}
		requireUnset(vmguestOnly, "vmguest")
		if sessionType == "winrm" {
			requireUnset(sshOnly, "ssh")
		} else {
			requireUnset(winrmOnly, "winrm")
		}
	case "vmguest":
		if !vmguestOnly["session_vm_name"] && !vmguestOnly["session_vm_id"] &&
			!config.SessionVMName.IsUnknown() && !config.SessionVMId.IsUnknown() {
			diags.AddAttributeError(
				path.Root("session_vm_name"),
				"Missing VM Identifier",
				"session_type \"vmguest\" requires session_vm_name or session_vm_id.",
			)
		}
		if vmguestOnly["session_vm_name"] && vmguestOnly["session_vm_id"] {
			diags.AddAttributeError(
				path.Root("session_vm_name"),
				"Conflicting VM Identifiers",
				"Set only one of session_vm_name or session_vm_id, not both.",
			)
		}
		requireUnset(winrmOnly, "winrm")
		requireUnset(sshOnly, "ssh")
		requireUnset(hostLike, "winrm or ssh")
	}
}

// buildSessionConfig returns a *PSSessionConfig when session_type is set, otherwise
// nil (scripts run locally). Only set fields are carried over.
func buildSessionConfig(config PowerShellProviderModel) *PSSessionConfig {
	sessionType := strOrEmpty(config.SessionType)
	if sessionType == "" {
		return nil
	}
	s := &PSSessionConfig{
		Type:              sessionType,
		Host:              strOrEmpty(config.SessionHost),
		Username:          strOrEmpty(config.SessionUsername),
		Password:          strOrEmpty(config.SessionPassword),
		Authentication:    strOrEmpty(config.SessionAuthentication),
		CertThumbprint:    strOrEmpty(config.SessionCertThumbprint),
		ConfigurationName: strOrEmpty(config.SessionConfigurationName),
		KeyFile:           strOrEmpty(config.SessionKeyFile),
		VMName:            strOrEmpty(config.SessionVMName),
		VMId:              strOrEmpty(config.SessionVMId),
	}
	if !config.SessionPort.IsNull() && !config.SessionPort.IsUnknown() {
		s.Port = config.SessionPort.ValueInt64()
	}
	if !config.SessionUseSSL.IsNull() && !config.SessionUseSSL.IsUnknown() {
		s.UseSSL = config.SessionUseSSL.ValueBool()
	}
	return s
}

// Shutdown runs the registered shutdown script (if any) and terminates the
// persistent PowerShell process. It is safe to call multiple times; the
// shutdown script runs at most once. Serving must invoke this when it stops
// (see Factory and main) because the plugin framework has no teardown hook.
func (p *PowerShellProvider) Shutdown(_ context.Context) error {
	if p.psManager == nil {
		return nil
	}
	return p.psManager.Close()
}

// Resources lists the resource types this provider implements. Terraform calls
// each constructor to instantiate a resource when one appears in configuration.
func (p *PowerShellProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewScriptResource,
	}
}

// DataSources lists the data sources this provider implements.
func (p *PowerShellProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewScriptDataSource,
	}
}
