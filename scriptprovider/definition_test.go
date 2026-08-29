package scriptprovider

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	fwpath "github.com/hashicorp/terraform-plugin-framework/path"
)

// testDefinitionFS builds an in-memory provider/ tree exercising the whole
// definition surface: custom provider attribute, lifecycle scripts, a resource
// with update.ps1, a resource without one, and a data source.
func testDefinitionFS() fstest.MapFS {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	return fstest.MapFS{
		"provider/settings.json": file(`{
			"name": "sampletest",
			"address": "registry.terraform.io/test/sampletest",
			"engine_version": "latest"
		}`),
		"provider/schema.json": file(`{
			"version": 1,
			"description": "Test provider.",
			"attributes": {
				"endpoint": {"type": "string", "optional": true, "env": "SAMPLETEST_ENDPOINT"}
			}
		}`),
		"provider/scripts/startup.ps1": file(
			`$global:DerivedTestState = @{ endpoint = $global:ProviderData.Config.endpoint }`),
		"provider/scripts/shutdown.ps1": file(
			`$global:DerivedTestState = $null`),
		"provider/resources/item/schema.json": file(`{
			"version": 1,
			"description": "An item.",
			"attributes": {
				"name":   {"type": "string", "required": true},
				"size":   {"type": "int", "optional": true, "default": 4},
				"status": {"type": "string", "computed": true},
				"things": {"type": "list", "element_type": "string", "computed": true}
			}
		}`),
		"provider/resources/item/create.ps1": file(
			`@{ id = "item-$($InputData.name)"; status = "created:$($InputData.size):$($global:DerivedTestState.endpoint)"; things = @("a", "b") }`),
		"provider/resources/item/read.ps1": file(
			`@{ id = $InputData.id; status = "read"; things = @("a") }`),
		"provider/resources/item/update.ps1": file(
			`@{ id = $InputData.id; status = "updated:$($InputData.name)"; things = @("c") }`),
		"provider/resources/item/delete.ps1": file(
			`@{ id = $InputData.id }`),
		// A resource without update.ps1: config changes must force replacement.
		"provider/resources/fixed/schema.json": file(`{
			"version": 1,
			"attributes": {
				"name":   {"type": "string", "required": true},
				"status": {"type": "string", "computed": true}
			}
		}`),
		"provider/resources/fixed/create.ps1": file(`@{ id = "fixed-$($InputData.name)"; status = "ok" }`),
		"provider/resources/fixed/read.ps1":   file(`@{ id = $InputData.id; status = "ok" }`),
		"provider/resources/fixed/delete.ps1": file(`@{ id = $InputData.id }`),
		"provider/data-sources/lookup/schema.json": file(`{
			"version": 1,
			"attributes": {
				"query":  {"type": "string", "required": true},
				"result": {"type": "string", "computed": true}
			}
		}`),
		"provider/data-sources/lookup/read.ps1": file(
			`@{ result = "found:$($InputData.query)" }`),
	}
}

func TestLoadSettings(t *testing.T) {
	s, err := LoadSettings(testDefinitionFS())
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.Name != "sampletest" || s.Address != "registry.terraform.io/test/sampletest" {
		t.Errorf("unexpected settings: %+v", s)
	}
}

func TestLoadDefinition(t *testing.T) {
	pd, err := loadDefinition(Definition{
		Settings: Settings{Name: "sampletest", Address: "registry.terraform.io/test/sampletest"},
		Version:  "0.0.1",
		FS:       testDefinitionFS(),
	})
	if err != nil {
		t.Fatalf("loadDefinition: %v", err)
	}
	if len(pd.resources) != 2 {
		t.Fatalf("resources = %d, want 2", len(pd.resources))
	}
	if len(pd.dataSources) != 1 {
		t.Fatalf("dataSources = %d, want 1", len(pd.dataSources))
	}
	// Sorted discovery: fixed before item.
	if pd.resources[0].Name != "fixed" || pd.resources[1].Name != "item" {
		t.Errorf("resource order: %s, %s", pd.resources[0].Name, pd.resources[1].Name)
	}
	if pd.resources[0].Scripts.Update != "" {
		t.Error("fixed should have no update script")
	}
	if pd.resources[1].Scripts.Update == "" {
		t.Error("item should have an update script")
	}
	if pd.startupScript == "" || pd.shutdownScript == "" {
		t.Error("lifecycle scripts not loaded")
	}
	if pd.providerManifest == nil || len(pd.customAttrs) != 1 {
		t.Error("provider manifest not loaded")
	}
}

func TestLoadDefinitionErrors(t *testing.T) {
	base := func() fstest.MapFS {
		fs := fstest.MapFS{}
		for k, v := range testDefinitionFS() {
			fs[k] = v
		}
		return fs
	}

	t.Run("missing create script", func(t *testing.T) {
		fs := base()
		delete(fs, "provider/resources/item/create.ps1")
		_, err := loadDefinition(Definition{Settings: Settings{Name: "x", Address: "a/b/x"}, FS: fs})
		if err == nil || !strings.Contains(err.Error(), "create.ps1") {
			t.Errorf("expected missing create.ps1 error, got: %v", err)
		}
	})

	t.Run("builtin attribute collision", func(t *testing.T) {
		fs := base()
		fs["provider/schema.json"] = &fstest.MapFile{Data: []byte(`{
			"version": 1,
			"attributes": {"timeout": {"type": "int", "optional": true}}
		}`)}
		_, err := loadDefinition(Definition{Settings: Settings{Name: "x", Address: "a/b/x"}, FS: fs})
		if err == nil || !strings.Contains(err.Error(), "collides with a built-in") {
			t.Errorf("expected collision error, got: %v", err)
		}
	})

	t.Run("bad provider name", func(t *testing.T) {
		_, err := loadDefinition(Definition{Settings: Settings{Name: "Bad-Name", Address: "a/b/x"}, FS: base()})
		if err == nil || !strings.Contains(err.Error(), "must match") {
			t.Errorf("expected name error, got: %v", err)
		}
	})

	t.Run("reserved id attribute", func(t *testing.T) {
		fs := base()
		fs["provider/resources/item/schema.json"] = &fstest.MapFile{Data: []byte(`{
			"version": 1,
			"attributes": {"id": {"type": "string", "computed": true}}
		}`)}
		_, err := loadDefinition(Definition{Settings: Settings{Name: "x", Address: "a/b/x"}, FS: fs})
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("expected reserved-id error, got: %v", err)
		}
	})
}

// configureDefinitionProvider spins up a definition provider with a live
// sidecar, registering teardown. It returns the configured provider.
func configureDefinitionProvider(t *testing.T, endpoint string) *definitionProvider {
	t.Helper()
	ctx := context.Background()

	newProvider, factory, err := NewProviderFactory(Definition{
		Settings: Settings{Name: "sampletest", Address: "registry.terraform.io/test/sampletest"},
		Version:  "0.0.1-test",
		FS:       testDefinitionFS(),
	})
	if err != nil {
		t.Fatalf("NewProviderFactory: %v", err)
	}
	p := newProvider().(*definitionProvider)
	t.Cleanup(func() { factory.Shutdown(context.Background()) })

	schemaResp := &fwprovider.SchemaResponse{}
	p.Schema(ctx, fwprovider.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("provider schema: %v", schemaResp.Diagnostics)
	}
	if _, ok := schemaResp.Schema.Attributes["endpoint"]; !ok {
		t.Fatal("custom attribute endpoint missing from provider schema")
	}

	attrs := nullProviderAttrs()
	attrs["endpoint"] = tftypes.NewValue(tftypes.String, nullableString(endpoint))
	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	raw := tftypes.NewValue(objType, attrs)

	configureResp := &fwprovider.ConfigureResponse{}
	p.Configure(ctx, fwprovider.ConfigureRequest{
		Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: raw},
	}, configureResp)
	if configureResp.Diagnostics.HasError() {
		t.Fatalf("provider configure: %v", configureResp.Diagnostics)
	}
	return p
}

// itemResource instantiates and configures the "item" dynamic resource.
func itemResource(t *testing.T, p *definitionProvider, name string) (resource.Resource, tftypes.Object) {
	t.Helper()
	ctx := context.Background()
	var target resource.Resource
	for _, fn := range p.Resources(ctx) {
		r := fn()
		metaResp := &resource.MetadataResponse{}
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "sampletest"}, metaResp)
		if metaResp.TypeName == "sampletest_"+name {
			target = r
			break
		}
	}
	if target == nil {
		t.Fatalf("resource %s not registered", name)
	}
	confResp := &resource.ConfigureResponse{}
	target.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: p}, confResp)
	if confResp.Diagnostics.HasError() {
		t.Fatalf("resource configure: %v", confResp.Diagnostics)
	}
	schemaResp := &resource.SchemaResponse{}
	target.Schema(ctx, resource.SchemaRequest{}, schemaResp)
	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	return target, objType
}

func stateString(t *testing.T, st tfsdk.State, name string) string {
	t.Helper()
	var v types.String
	if diags := st.GetAttribute(context.Background(), fwpath.Root(name), &v); diags.HasError() {
		t.Fatalf("get %s: %v", name, diags)
	}
	return v.ValueString()
}

// TestDefinitionProviderLifecycle drives a full CRUD cycle plus a data source
// read through the dynamic layer against a real sidecar: typed config →
// $InputData, emitted objects → typed computed attributes, default injection,
// custom provider attributes via $global:ProviderData.Config, and the
// definition startup script.
func TestDefinitionProviderLifecycle(t *testing.T) {
	ctx := context.Background()
	p := configureDefinitionProvider(t, "ep1")
	r, objType := itemResource(t, p, "item")

	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	listUnknown := tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, tftypes.UnknownValue)
	rSchemaResp := &resource.SchemaResponse{}
	r.Schema(ctx, resource.SchemaRequest{}, rSchemaResp)
	rSchema := rSchemaResp.Schema

	// --- Create ---------------------------------------------------------
	planRaw := tftypes.NewValue(objType, map[string]tftypes.Value{
		"id":     unknown,
		"name":   tftypes.NewValue(tftypes.String, "foo"),
		"size":   tftypes.NewValue(tftypes.Number, nil), // null → default 4 injected into $InputData
		"status": unknown,
		"things": listUnknown,
	})
	createResp := &resource.CreateResponse{State: tfsdk.State{Schema: rSchema, Raw: tftypes.NewValue(objType, nil)}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: rSchema, Raw: planRaw}}, createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create: %v", createResp.Diagnostics)
	}
	if got := stateString(t, createResp.State, "id"); got != "item-foo" {
		t.Errorf("id = %q, want item-foo", got)
	}
	if got := stateString(t, createResp.State, "status"); got != "created:4:ep1" {
		t.Errorf("status = %q, want created:4:ep1 (default + Config delivery)", got)
	}
	var things types.List
	if diags := createResp.State.GetAttribute(ctx, fwpath.Root("things"), &things); diags.HasError() {
		t.Fatalf("things: %v", diags)
	}
	if len(things.Elements()) != 2 {
		t.Errorf("things = %v, want 2 elements", things.Elements())
	}

	// --- Read -----------------------------------------------------------
	readResp := &resource.ReadResponse{State: tfsdk.State{Schema: rSchema, Raw: createResp.State.Raw}}
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: rSchema, Raw: createResp.State.Raw}}, readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read: %v", readResp.Diagnostics)
	}
	if got := stateString(t, readResp.State, "status"); got != "read" {
		t.Errorf("status after read = %q, want read", got)
	}
	// Config attributes come from state, never from script output.
	if got := stateString(t, readResp.State, "name"); got != "foo" {
		t.Errorf("name after read = %q, want foo", got)
	}

	// --- Update ---------------------------------------------------------
	updatePlan := tftypes.NewValue(objType, map[string]tftypes.Value{
		"id":     tftypes.NewValue(tftypes.String, "item-foo"),
		"name":   tftypes.NewValue(tftypes.String, "bar"),
		"size":   tftypes.NewValue(tftypes.Number, nil),
		"status": unknown,
		"things": listUnknown,
	})
	updateResp := &resource.UpdateResponse{State: tfsdk.State{Schema: rSchema, Raw: tftypes.NewValue(objType, nil)}}
	r.Update(ctx, resource.UpdateRequest{
		Plan:  tfsdk.Plan{Schema: rSchema, Raw: updatePlan},
		State: tfsdk.State{Schema: rSchema, Raw: createResp.State.Raw},
	}, updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update: %v", updateResp.Diagnostics)
	}
	if got := stateString(t, updateResp.State, "status"); got != "updated:bar" {
		t.Errorf("status after update = %q, want updated:bar", got)
	}
	if got := stateString(t, updateResp.State, "id"); got != "item-foo" {
		t.Errorf("id after update = %q, want item-foo (carried from state)", got)
	}

	// --- Delete ---------------------------------------------------------
	deleteResp := &resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: tfsdk.State{Schema: rSchema, Raw: updateResp.State.Raw}}, deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("delete: %v", deleteResp.Diagnostics)
	}

	// --- Data source ----------------------------------------------------
	var ds datasource.DataSource
	for _, fn := range p.DataSources(ctx) {
		d := fn()
		metaResp := &datasource.MetadataResponse{}
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "sampletest"}, metaResp)
		if metaResp.TypeName == "sampletest_lookup" {
			ds = d
			break
		}
	}
	if ds == nil {
		t.Fatal("data source lookup not registered")
	}
	dsConfResp := &datasource.ConfigureResponse{}
	ds.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: p}, dsConfResp)
	dsSchemaResp := &datasource.SchemaResponse{}
	ds.Schema(ctx, datasource.SchemaRequest{}, dsSchemaResp)
	dsType := dsSchemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	dsConfig := tftypes.NewValue(dsType, map[string]tftypes.Value{
		"query":  tftypes.NewValue(tftypes.String, "q1"),
		"result": tftypes.NewValue(tftypes.String, nil),
	})
	dsReadResp := &datasource.ReadResponse{State: tfsdk.State{Schema: dsSchemaResp.Schema, Raw: tftypes.NewValue(dsType, nil)}}
	ds.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSchemaResp.Schema, Raw: dsConfig}}, dsReadResp)
	if dsReadResp.Diagnostics.HasError() {
		t.Fatalf("data source read: %v", dsReadResp.Diagnostics)
	}
	var result types.String
	if diags := dsReadResp.State.GetAttribute(ctx, fwpath.Root("result"), &result); diags.HasError() {
		t.Fatalf("result: %v", diags)
	}
	if result.ValueString() != "found:q1" {
		t.Errorf("result = %q, want found:q1", result.ValueString())
	}
}

// TestDynamicModifyPlan checks update-vs-replace semantics without a sidecar.
func TestDynamicModifyPlan(t *testing.T) {
	ctx := context.Background()
	pd, err := loadDefinition(Definition{
		Settings: Settings{Name: "sampletest", Address: "registry.terraform.io/test/sampletest"},
		FS:       testDefinitionFS(),
	})
	if err != nil {
		t.Fatalf("loadDefinition: %v", err)
	}

	build := func(rd *ResourceDefinition, stateVals, planVals map[string]tftypes.Value) (resource.ModifyPlanRequest, *resource.ModifyPlanResponse, *dynamicResource) {
		r := &dynamicResource{def: rd}
		schemaResp := &resource.SchemaResponse{}
		r.Schema(ctx, resource.SchemaRequest{}, schemaResp)
		objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
		req := resource.ModifyPlanRequest{
			State: tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objType, stateVals)},
			Plan:  tfsdk.Plan{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objType, planVals)},
		}
		resp := &resource.ModifyPlanResponse{Plan: req.Plan}
		return req, resp, r
	}

	// fixed (no update.ps1): a change to name must force replacement.
	var fixed *ResourceDefinition
	var item *ResourceDefinition
	for _, rd := range pd.resources {
		switch rd.Name {
		case "fixed":
			fixed = rd
		case "item":
			item = rd
		}
	}

	s := func(v string) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }
	req, resp, r := build(fixed,
		map[string]tftypes.Value{"id": s("fixed-a"), "name": s("a"), "status": s("ok")},
		map[string]tftypes.Value{"id": s("fixed-a"), "name": s("b"), "status": s("ok")})
	r.ModifyPlan(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan: %v", resp.Diagnostics)
	}
	if len(resp.RequiresReplace) != 1 || resp.RequiresReplace[0].String() != "name" {
		t.Errorf("RequiresReplace = %v, want [name]", resp.RequiresReplace)
	}

	// item (has update.ps1): computed attrs become unknown instead.
	listVal := tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{s("a")})
	n := tftypes.NewValue(tftypes.Number, nil)
	req2, resp2, r2 := build(item,
		map[string]tftypes.Value{"id": s("item-a"), "name": s("a"), "size": n, "status": s("ok"), "things": listVal},
		map[string]tftypes.Value{"id": s("item-a"), "name": s("b"), "size": n, "status": s("ok"), "things": listVal})
	r2.ModifyPlan(ctx, req2, resp2)
	if resp2.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan: %v", resp2.Diagnostics)
	}
	if len(resp2.RequiresReplace) != 0 {
		t.Errorf("RequiresReplace = %v, want none", resp2.RequiresReplace)
	}
	var status types.String
	if diags := resp2.Plan.GetAttribute(ctx, fwpath.Root("status"), &status); diags.HasError() {
		t.Fatalf("status: %v", diags)
	}
	if !status.IsUnknown() {
		t.Error("status should be unknown after ModifyPlan with update script")
	}

	// No config change: plan untouched.
	req3, resp3, r3 := build(item,
		map[string]tftypes.Value{"id": s("item-a"), "name": s("a"), "size": n, "status": s("ok"), "things": listVal},
		map[string]tftypes.Value{"id": s("item-a"), "name": s("a"), "size": n, "status": s("ok"), "things": listVal})
	r3.ModifyPlan(ctx, req3, resp3)
	if len(resp3.RequiresReplace) != 0 {
		t.Errorf("RequiresReplace = %v, want none", resp3.RequiresReplace)
	}
	var status3 types.String
	_ = resp3.Plan.GetAttribute(ctx, fwpath.Root("status"), &status3)
	if status3.IsUnknown() {
		t.Error("status should remain known when nothing changed")
	}
}
