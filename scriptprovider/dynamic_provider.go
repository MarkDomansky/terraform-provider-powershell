package scriptprovider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	pschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Compile-time interface assertions for the definition-based provider.
var (
	_ provider.Provider                   = &definitionProvider{}
	_ provider.ProviderWithValidateConfig = &definitionProvider{}
	_ ShutdownProvider                    = &definitionProvider{}
)

// definitionProvider serves a derived provider from a loaded providerDefinition.
// It reuses the engine bootstrap (sidecar, remote sessions, startup/shutdown
// scripts) and registers one dynamic resource / data source per definition.
type definitionProvider struct {
	def       *providerDefinition
	psManager *PSManager // nil until Configure runs
}

// Metadata reports the derived provider's type name (from settings.tfps.json) and
// version. The type name prefixes every resource, e.g. "<name>_<resource>".
func (p *definitionProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = p.def.settings.Name
	resp.Version = p.def.version
}

// Schema is the engine's built-in provider attributes plus the definition's
// custom attributes (collisions were rejected at load time).
func (p *definitionProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	attrs := builtinProviderAttributes()
	for name, a := range p.def.customAttrs {
		attrs[name] = a
	}
	description := "PowerShell-script-backed provider built on the terraform-provider-powershell engine."
	if p.def.providerManifest != nil && p.def.providerManifest.Description != "" {
		description = p.def.providerManifest.Description
	}
	resp.Schema = pschema.Schema{
		Description: description,
		Attributes:  attrs,
	}
}

// decodeBuiltinModel reads the engine's built-in provider attributes one at a
// time. A derived provider's schema is a superset of PowerShellProviderModel,
// so a whole-config Get into the struct would fail on the custom attributes.
func decodeBuiltinModel(ctx context.Context, cfg attrGetter, model *PowerShellProviderModel) diag.Diagnostics {
	var diags diag.Diagnostics
	diags.Append(cfg.GetAttribute(ctx, path.Root("startup_script"), &model.StartupScript)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("shutdown_script"), &model.ShutdownScript)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("timeout"), &model.Timeout)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("server"), &model.Server)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("username"), &model.Username)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("password"), &model.Password)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("cert_thumbprint"), &model.CertThumbprint)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("provider_data"), &model.ProviderData)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("sensitive_provider_data"), &model.SensitiveProviderData)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_type"), &model.SessionType)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_host"), &model.SessionHost)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_port"), &model.SessionPort)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_username"), &model.SessionUsername)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_password"), &model.SessionPassword)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_use_ssl"), &model.SessionUseSSL)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_authentication"), &model.SessionAuthentication)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_cert_thumbprint"), &model.SessionCertThumbprint)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_configuration_name"), &model.SessionConfigurationName)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_key_file"), &model.SessionKeyFile)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_vm_name"), &model.SessionVMName)...)
	diags.Append(cfg.GetAttribute(ctx, path.Root("session_vm_id"), &model.SessionVMId)...)
	return diags
}

// ValidateConfig applies the engine's cross-attribute session rules; the
// framework validates the custom attributes from their schema declarations.
func (p *definitionProvider) ValidateConfig(ctx context.Context, req provider.ValidateConfigRequest, resp *provider.ValidateConfigResponse) {
	var model PowerShellProviderModel
	resp.Diagnostics.Append(decodeBuiltinModel(ctx, req.Config, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateSessionConfig(model, &resp.Diagnostics)
}

// Configure bootstraps the engine exactly like the generic provider, with two
// additions: the definition's custom attributes are decoded and delivered to
// scripts as $global:ProviderData.Config, and the definition's startup.ps1 /
// shutdown.ps1 wrap the practitioner's startup_script / shutdown_script.
func (p *definitionProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var model PowerShellProviderModel
	resp.Diagnostics.Append(decodeBuiltinModel(ctx, req.Config, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var extra map[string]interface{}
	if p.def.providerManifest != nil {
		var diags diag.Diagnostics
		extra, diags = inputDataFromConfig(ctx, p.def.providerManifest, req.Config)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		// Environment fallbacks for custom string attributes declaring "env",
		// mirroring the engine's built-in password fallbacks.
		for name, spec := range p.def.providerManifest.Attributes {
			if spec.Env == "" {
				continue
			}
			if _, set := extra[name]; set {
				continue
			}
			if v := os.Getenv(spec.Env); v != "" {
				extra[name] = v
			}
		}
	}

	psManager := configureEngine(ctx, model, extra, p.def.startupScript, p.def.shutdownScript, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	p.psManager = psManager

	resp.DataSourceData = p
	resp.ResourceData = p
}

// Resources registers one dynamic resource per provider/resources/<name> folder.
func (p *definitionProvider) Resources(_ context.Context) []func() resource.Resource {
	out := make([]func() resource.Resource, 0, len(p.def.resources))
	for _, rd := range p.def.resources {
		out = append(out, newDynamicResource(rd))
	}
	return out
}

// DataSources registers one dynamic data source per provider/data-sources/<name>.
func (p *definitionProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	out := make([]func() datasource.DataSource, 0, len(p.def.dataSources))
	for _, dd := range p.def.dataSources {
		out = append(out, newDynamicDataSource(dd))
	}
	return out
}

// Shutdown runs the registered shutdown scripts (practitioner's first, then
// the definition's) and stops the sidecar. Safe to call multiple times.
func (p *definitionProvider) Shutdown(_ context.Context) error {
	if p.psManager == nil {
		return nil
	}
	return p.psManager.Close()
}
