package scriptprovider

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// nullableString returns nil for an empty string (a null Terraform value) and
// the string itself otherwise.
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// nullProviderAttrs returns every provider-schema attribute set to a typed null.
// Tests start from this and override only the attributes they care about — the
// framework requires the raw object to carry a value for every declared attribute,
// so this keeps the config literals in sync as the schema grows.
func nullProviderAttrs() map[string]tftypes.Value {
	s := tftypes.NewValue(tftypes.String, nil)
	n := tftypes.NewValue(tftypes.Number, nil)
	b := tftypes.NewValue(tftypes.Bool, nil)
	return map[string]tftypes.Value{
		"startup_script":             s,
		"shutdown_script":            s,
		"timeout":                    n,
		"server":                     s,
		"username":                   s,
		"password":                   s,
		"cert_thumbprint":            s,
		"provider_data":              s,
		"sensitive_provider_data":    s,
		"session_type":               s,
		"session_host":               s,
		"session_port":               n,
		"session_username":           s,
		"session_password":           s,
		"session_use_ssl":            b,
		"session_authentication":     s,
		"session_cert_thumbprint":    s,
		"session_configuration_name": s,
		"session_key_file":           s,
		"session_vm_name":            s,
		"session_vm_id":              s,
	}
}

// configureWithAttrs runs the provider's real Configure path with the given
// attribute overrides (merged over nullProviderAttrs) and returns the configured
// provider, registering teardown.
func configureWithAttrs(t *testing.T, overrides map[string]tftypes.Value) *PowerShellProvider {
	t.Helper()
	ctx := context.Background()
	p := &PowerShellProvider{version: "test"}

	schemaResp := &fwprovider.SchemaResponse{}
	p.Schema(ctx, fwprovider.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("provider schema error: %v", schemaResp.Diagnostics)
	}

	attrs := nullProviderAttrs()
	for k, v := range overrides {
		attrs[k] = v
	}

	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	raw := tftypes.NewValue(objType, attrs)

	req := fwprovider.ConfigureRequest{
		Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: raw},
	}
	resp := &fwprovider.ConfigureResponse{}
	p.Configure(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("provider Configure failed: %v", resp.Diagnostics)
	}
	if p.psManager == nil {
		t.Fatal("provider Configure did not initialize the PowerShell process")
	}

	// Ensure the process is torn down even if the test fails before Shutdown.
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p
}

// configurePowerShellProvider runs the provider's real Configure path with the
// given startup and shutdown scripts, returning the configured provider. This
// exercises the same code Terraform uses, so the startup script runs exactly
// once here and the shutdown script is registered for teardown.
func configurePowerShellProvider(t *testing.T, startup, shutdown string) *PowerShellProvider {
	t.Helper()
	return configureWithAttrs(t, map[string]tftypes.Value{
		"startup_script":  tftypes.NewValue(tftypes.String, nullableString(startup)),
		"shutdown_script": tftypes.NewValue(tftypes.String, nullableString(shutdown)),
	})
}

// TestProviderStartupAndShutdownSharedAcrossResources verifies the end-to-end
// provider contract:
//   - the startup script runs exactly once during Configure, regardless of how
//     many resources subsequently run;
//   - every resource operation executes in that same single PowerShell process;
//   - the shutdown script runs exactly once at teardown, in the same process,
//     and can observe state accumulated by all the resources.
func TestProviderStartupAndShutdownSharedAcrossResources(t *testing.T) {
	dir := t.TempDir()
	startupLog := filepath.ToSlash(filepath.Join(dir, "startup.log"))
	shutdownLog := filepath.ToSlash(filepath.Join(dir, "shutdown.log"))

	startup := `
		Add-Content -Path '` + startupLog + `' -Value "startup pid=$PID"
		$global:HostPid = "$PID"
		$global:StartupCount = [int]$global:StartupCount + 1
		$global:ResourceCount = 0
	`
	shutdown := `
		Add-Content -Path '` + shutdownLog + `' -Value "pid=$PID startup_count=$($global:StartupCount) resource_count=$($global:ResourceCount)"
	`

	p := configurePowerShellProvider(t, startup, shutdown)

	// Startup ran exactly once during Configure.
	if got := countLines(t, startupLog); got != 1 {
		t.Fatalf("expected startup script to run once during Configure, ran %d times", got)
	}
	// Shutdown has not run yet.
	if got := countLines(t, shutdownLog); got != 0 {
		t.Fatalf("shutdown script ran before teardown (%d times)", got)
	}

	// Drive several resources through the provider's shared PowerShell process.
	// This mirrors what ScriptResource.Create/Read/Update/Delete do internally.
	const resourceCount = 4
	var hostPID string
	for i := 0; i < resourceCount; i++ {
		resp, err := p.psManager.Execute("create", `
			$global:ResourceCount = [int]$global:ResourceCount + 1
			[PSCustomObject]@{
				pid           = "$PID"
				host_pid      = "$($global:HostPid)"
				startup_count = $global:StartupCount
			}
		`, map[string]interface{}{"index": i}, 0)
		if err != nil {
			t.Fatalf("resource %d execute failed: %v", i, err)
		}
		if !resp.Success {
			t.Fatalf("resource %d script error: %s", i, resp.Error)
		}

		pid := fmt.Sprintf("%v", resp.OutputData["pid"])
		host := fmt.Sprintf("%v", resp.OutputData["host_pid"])
		if pid != host {
			t.Errorf("resource %d ran in a different process than startup: pid=%s startup pid=%s", i, pid, host)
		}
		if i == 0 {
			hostPID = pid
		} else if pid != hostPID {
			t.Errorf("resource %d pid=%s differs from earlier resources (pid=%s); not the same PowerShell instance", i, pid, hostPID)
		}
		// Resources never re-run startup, so the count they observe stays 1.
		if c := fmt.Sprintf("%v", resp.OutputData["startup_count"]); c != "1" {
			t.Errorf("resource %d observed startup_count=%s, want 1 (startup ran more than once)", i, c)
		}
	}

	// Startup still ran only once after all resource operations.
	if got := countLines(t, startupLog); got != 1 {
		t.Fatalf("startup script ran %d times across resources, want exactly 1", got)
	}

	// Tear the provider down twice; the shutdown script must run exactly once.
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("provider Shutdown failed: %v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("second provider Shutdown failed: %v", err)
	}

	if got := countLines(t, shutdownLog); got != 1 {
		t.Fatalf("expected shutdown script to run exactly once, ran %d times", got)
	}

	// The shutdown script shared the same process and provider state as every
	// resource: same PID, startup seen once, and all resource ops accounted for.
	line := readTrimmed(t, shutdownLog)
	if !strings.Contains(line, "pid="+hostPID+" ") {
		t.Errorf("shutdown ran in a different process than the resources: line=%q, want pid=%s", line, hostPID)
	}
	if !strings.Contains(line, "startup_count=1") {
		t.Errorf("shutdown observed wrong startup_count: line=%q, want startup_count=1", line)
	}
	if !strings.Contains(line, fmt.Sprintf("resource_count=%d", resourceCount)) {
		t.Errorf("shutdown did not observe all resource operations: line=%q, want resource_count=%d", line, resourceCount)
	}
}

// TestProviderFactoryShutsDownInstances verifies the serving Factory tracks the
// providers it creates and runs each one's shutdown script when Shutdown is
// called (this is what main does once providerserver.Serve returns).
func TestProviderFactoryShutsDownInstances(t *testing.T) {
	dir := t.TempDir()
	shutdownLog := filepath.ToSlash(filepath.Join(dir, "shutdown.log"))

	factory := NewFactory("test")
	p := factory.New()().(*PowerShellProvider)

	ctx := context.Background()
	schemaResp := &fwprovider.SchemaResponse{}
	p.Schema(ctx, fwprovider.SchemaRequest{}, schemaResp)
	attrs := nullProviderAttrs()
	attrs["shutdown_script"] = tftypes.NewValue(tftypes.String, `Add-Content -Path '`+shutdownLog+`' -Value "factory shutdown"`)
	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	raw := tftypes.NewValue(objType, attrs)
	resp := &fwprovider.ConfigureResponse{}
	p.Configure(ctx, fwprovider.ConfigureRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: raw}}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure failed: %v", resp.Diagnostics)
	}

	factory.Shutdown(ctx)

	if got := countLines(t, shutdownLog); got != 1 {
		t.Fatalf("expected factory Shutdown to run the shutdown script once, ran %d times", got)
	}
}

// TestProviderDataExposedToResources verifies the connection arguments configured
// on the provider block are decoded and surfaced to resource scripts via
// $global:ProviderData, including the nested Data object from the provider_data
// JSON string and the SensitiveData object from sensitive_provider_data.
func TestProviderDataExposedToResources(t *testing.T) {
	p := configureWithAttrs(t, map[string]tftypes.Value{
		"server":                  tftypes.NewValue(tftypes.String, "db01.example.com"),
		"username":                tftypes.NewValue(tftypes.String, "svc-deploy"),
		"provider_data":           tftypes.NewValue(tftypes.String, `{"environment":"prod","retries":3}`),
		"sensitive_provider_data": tftypes.NewValue(tftypes.String, `{"api_key":"s3cret"}`),
	})

	resp, err := p.psManager.Execute("create", `
		[PSCustomObject]@{
			id          = "res-1"
			server      = $global:ProviderData.server
			username    = $global:ProviderData.username
			environment = $global:ProviderData.Data.environment
			api_key     = $global:ProviderData.SensitiveData.api_key
		}
	`, nil, 0)
	if err != nil {
		t.Fatalf("resource execute failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("resource script error: %s", resp.Error)
	}
	if got := fmt.Sprintf("%v", resp.OutputData["server"]); got != "db01.example.com" {
		t.Errorf("ProviderData.server = %q, want db01.example.com", got)
	}
	if got := fmt.Sprintf("%v", resp.OutputData["username"]); got != "svc-deploy" {
		t.Errorf("ProviderData.username = %q, want svc-deploy", got)
	}
	if got := fmt.Sprintf("%v", resp.OutputData["environment"]); got != "prod" {
		t.Errorf("ProviderData.Data.environment = %q, want prod", got)
	}
	if got := fmt.Sprintf("%v", resp.OutputData["api_key"]); got != "s3cret" {
		t.Errorf("ProviderData.SensitiveData.api_key = %q, want s3cret", got)
	}
}
