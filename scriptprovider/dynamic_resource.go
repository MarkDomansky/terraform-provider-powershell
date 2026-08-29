package scriptprovider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Compile-time interface assertions for the dynamic resource.
var (
	_ resource.Resource                = &dynamicResource{}
	_ resource.ResourceWithConfigure   = &dynamicResource{}
	_ resource.ResourceWithModifyPlan  = &dynamicResource{}
	_ resource.ResourceWithImportState = &dynamicResource{}
)

// newDynamicResource returns a constructor closing over one parsed resource
// definition. A single Go type serves every manifest: schema, conversion, and
// plan behavior are all driven by the definition.
func newDynamicResource(def *ResourceDefinition) func() resource.Resource {
	return func() resource.Resource {
		return &dynamicResource{def: def}
	}
}

type dynamicResource struct {
	def      *ResourceDefinition
	provider *definitionProvider // set by Configure
}

func (r *dynamicResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.def.Name
}

func (r *dynamicResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = r.def.Schema
}

func (r *dynamicResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return // provider not configured yet (e.g. during validation)
	}
	p, ok := req.ProviderData.(*definitionProvider)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *definitionProvider, got: %T. This is a bug in the provider.", req.ProviderData),
		)
		return
	}
	r.provider = p
}

// stateID reads the "id" attribute from prior state.
func stateID(ctx context.Context, src attrGetter) (string, error) {
	var id types.String
	if diags := src.GetAttribute(ctx, path.Root("id"), &id); diags.HasError() {
		return "", fmt.Errorf("could not read id from state")
	}
	if id.IsNull() || id.IsUnknown() {
		return "", nil
	}
	return id.ValueString(), nil
}

// Create runs create.ps1 with $InputData built from the planned config
// attributes, requires a non-empty emitted id, and maps the emitted object
// onto the computed attributes.
func (r *dynamicResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	input, diags := inputDataFromConfig(ctx, r.def.Manifest, req.Plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Executing create script", map[string]interface{}{"resource": r.def.Name})
	result, err := r.provider.psManager.Execute("create", r.def.Scripts.Create, input, r.def.Timeout)
	if err != nil {
		resp.Diagnostics.AddError("Create Failed",
			fmt.Sprintf("Failed to execute create script: %s", err))
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Create Failed",
			fmt.Sprintf("Create script returned an error: %s", result.Error))
		return
	}

	rawID, hasID := result.OutputData["id"]
	if !hasID {
		resp.Diagnostics.AddError("Create Script Missing ID",
			"The create script must emit an object with an 'id' key that uniquely identifies the resource.")
		return
	}
	id := fmt.Sprintf("%v", rawID)
	if id == "" {
		resp.Diagnostics.AddError("Create Script Returned Empty ID",
			"The create script emitted an 'id' key with an empty value; the id must be a non-empty unique identifier.")
		return
	}

	// Seed state from the plan (config attributes), then overlay id and the
	// computed attributes from the emitted object.
	resp.State.Raw = req.Plan.Raw
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(id))...)
	resp.Diagnostics.Append(applyOutputData(ctx, r.def.Manifest, result.OutputData, &resp.State)...)
}

// Read runs read.ps1 with $InputData built from state plus the injected id.
// Emitting nothing (or no id) signals the resource no longer exists. Only
// computed attributes are refreshed: config attributes always come from state,
// mirroring the generic script resource.
func (r *dynamicResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	id, err := stateID(ctx, req.State)
	if err != nil || id == "" {
		resp.Diagnostics.AddError("Read Failed", "Resource state has no id.")
		return
	}

	input, diags := inputDataFromConfig(ctx, r.def.Manifest, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	input["id"] = id

	tflog.Debug(ctx, "Executing read script", map[string]interface{}{"resource": r.def.Name, "id": id})
	result, execErr := r.provider.psManager.Execute("read", r.def.Scripts.Read, input, r.def.Timeout)
	if execErr != nil {
		resp.Diagnostics.AddError("Read Failed",
			fmt.Sprintf("Failed to execute read script: %s", execErr))
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Read Failed",
			fmt.Sprintf("Read script returned an error: %s", result.Error))
		return
	}

	// Empty emission, or an emitted object without a usable id, means the
	// remote object is gone: remove the resource so Terraform plans recreation.
	if len(result.OutputData) == 0 {
		tflog.Info(ctx, "Read script emitted nothing; removing resource from state", map[string]interface{}{"id": id})
		resp.State.RemoveResource(ctx)
		return
	}
	if rawID, hasID := result.OutputData["id"]; hasID {
		if fmt.Sprintf("%v", rawID) == "" {
			tflog.Info(ctx, "Read script emitted an empty id; removing resource from state", map[string]interface{}{"id": id})
			resp.State.RemoveResource(ctx)
			return
		}
	} else {
		tflog.Info(ctx, "Read script emitted no id; removing resource from state", map[string]interface{}{"id": id})
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(applyOutputData(ctx, r.def.Manifest, result.OutputData, &resp.State)...)
}

// Update runs update.ps1 with $InputData built from the planned config
// attributes plus the id from prior state. It is only reached when config
// actually changed and update.ps1 exists (otherwise ModifyPlan forced a
// replacement), so it always executes the script.
func (r *dynamicResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	id, err := stateID(ctx, req.State)
	if err != nil || id == "" {
		resp.Diagnostics.AddError("Update Failed", "Resource state has no id.")
		return
	}

	input, diags := inputDataFromConfig(ctx, r.def.Manifest, req.Plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	input["id"] = id

	tflog.Debug(ctx, "Executing update script", map[string]interface{}{"resource": r.def.Name, "id": id})
	result, execErr := r.provider.psManager.Execute("update", r.def.Scripts.Update, input, r.def.Timeout)
	if execErr != nil {
		resp.Diagnostics.AddError("Update Failed",
			fmt.Sprintf("Failed to execute update script: %s", execErr))
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Update Failed",
			fmt.Sprintf("Update script returned an error: %s", result.Error))
		return
	}

	resp.State.Raw = req.Plan.Raw
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(id))...)
	resp.Diagnostics.Append(applyOutputData(ctx, r.def.Manifest, result.OutputData, &resp.State)...)
}

// Delete runs delete.ps1 with $InputData built from state plus the id.
func (r *dynamicResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	id, err := stateID(ctx, req.State)
	if err != nil || id == "" {
		// Nothing usable to delete; let Terraform drop the state entry.
		return
	}

	input, diags := inputDataFromConfig(ctx, r.def.Manifest, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	input["id"] = id

	tflog.Debug(ctx, "Executing delete script", map[string]interface{}{"resource": r.def.Name, "id": id})
	result, execErr := r.provider.psManager.Execute("delete", r.def.Scripts.Delete, input, r.def.Timeout)
	if execErr != nil {
		resp.Diagnostics.AddError("Delete Failed",
			fmt.Sprintf("Failed to execute delete script: %s", execErr))
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Delete Failed",
			fmt.Sprintf("Delete script returned an error: %s", result.Error))
		return
	}
}

// ModifyPlan implements the update-vs-replace semantics: when any config
// attribute changes and the resource has no update.ps1, every changed
// attribute requires replacement; when update.ps1 exists, the computed
// attributes (except id, which is stable) become unknown because the script
// will recompute them during apply.
func (r *dynamicResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Nothing to adjust when creating (no prior state) or destroying (no plan).
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}

	var changed []path.Path
	for _, name := range r.def.Manifest.configNames() {
		spec := r.def.Manifest.Attributes[name]
		p := path.Root(name)
		planVal, d1 := getTypedAttribute(ctx, req.Plan, p, nullValueFor(spec))
		resp.Diagnostics.Append(d1...)
		stateVal, d2 := getTypedAttribute(ctx, req.State, p, nullValueFor(spec))
		resp.Diagnostics.Append(d2...)
		if resp.Diagnostics.HasError() {
			return
		}
		if !planVal.Equal(stateVal) {
			changed = append(changed, p)
		}
	}
	if len(changed) == 0 {
		return
	}

	if r.def.Scripts.Update == "" {
		// No update.ps1: config changes can only be honored by replacing.
		resp.RequiresReplace = append(resp.RequiresReplace, changed...)
		return
	}

	// update.ps1 will run: computed attribute values after apply are unknowable
	// at plan time.
	for _, name := range r.def.Manifest.computedNames() {
		spec := r.def.Manifest.Attributes[name]
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(name), unknownValueFor(spec))...)
	}
}

// ImportState imports by id; the subsequent Read refreshes the computed
// attributes from the read script's emitted object.
func (r *dynamicResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
