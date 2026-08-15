package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Compile-time assertions that ScriptResource implements the framework
// interfaces it relies on: the base Resource contract, import support, and
// plan modification (replacement/unknown-output logic).
var (
	_ resource.Resource                = &ScriptResource{}
	_ resource.ResourceWithImportState = &ScriptResource{}
	_ resource.ResourceWithModifyPlan  = &ScriptResource{}
)

// ScriptResource implements the powershell_script Terraform resource. It holds a
// back-reference to the provider so it can reach the shared psManager and run its
// CRUD scripts in the same persistent PowerShell process as every other resource.
type ScriptResource struct {
	provider *PowerShellProvider
}

// ScriptResourceModel maps the resource block's HCL attributes onto Go fields.
// The `tfsdk` tags must match the attribute names declared in Schema.
type ScriptResourceModel struct {
	ID                  types.String `tfsdk:"id"`
	CreateScript        types.String `tfsdk:"create_script"`
	ReadScript          types.String `tfsdk:"read_script"`
	UpdateScript        types.String `tfsdk:"update_script"`
	DeleteScript        types.String `tfsdk:"delete_script"`
	InputData           types.String `tfsdk:"input_data"`
	SensitiveInputData  types.String `tfsdk:"sensitive_input_data"`
	OutputData          types.String `tfsdk:"output_data"`
	SensitiveOutputData types.String `tfsdk:"sensitive_output_data"`
	Triggers            types.Map    `tfsdk:"triggers"`
	Timeout             types.Int64  `tfsdk:"timeout"`
}

// NewScriptResource is the constructor Terraform calls (via the provider's
// Resources list) to create a fresh resource instance.
func NewScriptResource() resource.Resource {
	return &ScriptResource{}
}

// Metadata sets the resource's type name by combining the provider prefix with
// "_script", yielding "powershell_script".
func (r *ScriptResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_script"
}

// Schema declares the attributes of a powershell_script resource and how they
// behave during planning (e.g. which changes force a replacement).
func (r *ScriptResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version: 1,
		Description: "Manages a resource via PowerShell CRUD scriptblocks. " +
			"Each lifecycle action (create, read, update, delete) is handled by a separate PowerShell script. " +
			"Scripts receive input via the $InputData parameter and return their result by emitting a single " +
			"object (e.g. [PSCustomObject]@{ id = ...; ... }) to the output stream. " +
			"All data exchange uses JSON for consistency.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The resource identifier, taken from the 'id' field of the object the create script emits.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"create_script": schema.StringAttribute{
				Description: "PowerShell script executed on resource creation. " +
					"Must emit a single object with at least an 'id' key to the output stream. " +
					"Changing the script is an in-place config update and does not replace the resource; " +
					"use triggers to force recreation.",
				Required: true,
			},
			"read_script": schema.StringAttribute{
				Description: "PowerShell script executed to read the current resource state. " +
					"Must emit a single object with the current state. Emit nothing if the resource no longer exists.",
				Required: true,
			},
			"update_script": schema.StringAttribute{
				Description: "PowerShell script executed when input_data or sensitive_input_data change. " +
					"Must emit a single object with the updated state to the output stream. " +
					"When omitted, the resource is immutable: input changes force a replacement (delete then create).",
				Optional: true,
			},
			"delete_script": schema.StringAttribute{
				Description: "PowerShell script executed to destroy the resource.",
				Required:    true,
			},
			"input_data": schema.StringAttribute{
				Description: "JSON string of input data passed to scripts as the $InputData parameter (a hashtable). " +
					"Use jsonencode() to construct this value. Stored in state and shown in plan output; " +
					"put secrets in sensitive_input_data instead.",
				Optional: true,
				Validators: []validator.String{
					jsonObjectValidator{},
				},
			},
			"sensitive_input_data": schema.StringAttribute{
				Description: "JSON string of sensitive input data, merged into $InputData after input_data " +
					"(on key collision the sensitive value wins). Use jsonencode() to construct this value. " +
					"Redacted from plan output; note the value is still stored in Terraform state, so use an " +
					"encrypted state backend for secrets.",
				Optional:  true,
				Sensitive: true,
				Validators: []validator.String{
					jsonObjectValidator{},
				},
			},
			"output_data": schema.StringAttribute{
				Description: "JSON string of the object the scripts emitted to the output stream, excluding the " +
					"reserved 'sensitive' key. Use jsondecode() to extract values.",
				Computed: true,
			},
			"sensitive_output_data": schema.StringAttribute{
				Description: "JSON string of the value under the reserved 'sensitive' key of the object the scripts " +
					"emitted (\"{}\" when the key is absent). Redacted from plan output; still stored in state. " +
					"Use jsondecode() to extract values.",
				Computed:  true,
				Sensitive: true,
			},
			"triggers": schema.MapAttribute{
				Description: "A map of values that, when changed, force the resource to be recreated. " +
					"Useful for tracking script content hashes.",
				Optional:    true,
				ElementType: types.StringType,
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"timeout": schema.Int64Attribute{
				Description: "Timeout in seconds for script execution. Overrides the provider-level timeout.",
				Optional:    true,
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
		},
	}
}

// Configure receives the configured provider (set as resp.ResourceData in the
// provider's Configure) and stores it so CRUD methods can use its psManager.
func (r *ScriptResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// ProviderData is nil during early framework calls (e.g. validation) before
	// the provider has been configured; nothing to wire up yet.
	if req.ProviderData == nil {
		return
	}
	p, ok := req.ProviderData.(*PowerShellProvider)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *PowerShellProvider, got: %T", req.ProviderData),
		)
		return
	}
	r.provider = p
}

// ModifyPlan implements the update-vs-replace semantics:
//   - without an update_script, input changes can only be realized by replacing
//     the resource (delete then create);
//   - with an update_script, input changes run it in place, so the computed
//     outputs are unknowable at plan time and must be marked unknown (otherwise
//     Terraform errors when the applied outputs differ from prior state).
func (r *ScriptResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Nothing to decide on destroy (null plan) or create (null state).
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}

	var plan, state ScriptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	inputChanged := !plan.InputData.Equal(state.InputData) ||
		!plan.SensitiveInputData.Equal(state.SensitiveInputData)
	if !inputChanged {
		return
	}

	hasUpdateScript := !plan.UpdateScript.IsNull() && !plan.UpdateScript.IsUnknown() &&
		plan.UpdateScript.ValueString() != ""
	if !hasUpdateScript {
		if !plan.InputData.Equal(state.InputData) {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("input_data"))
		}
		if !plan.SensitiveInputData.Equal(state.SensitiveInputData) {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("sensitive_input_data"))
		}
		return
	}

	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("output_data"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("sensitive_output_data"), types.StringUnknown())...)
}

// Create runs the create_script, requires it to return an id, and records the
// returned id and output in Terraform state.
func (r *ScriptResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Read the planned values for the new resource.
	var data ScriptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	inputData, ok := r.buildInputData(data, &resp.Diagnostics)
	if !ok {
		return
	}
	timeout := timeoutFromAttr(data.Timeout)

	tflog.Debug(ctx, "Executing create script")
	result, err := r.provider.psManager.Execute("create", data.CreateScript.ValueString(), inputData, timeout)
	if err != nil {
		resp.Diagnostics.AddError("Create script execution failed", err.Error())
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Create script returned an error", result.Error)
		return
	}

	// The create script is contractually required to return an id; it becomes the
	// resource's Terraform identifier and is fed back to read/update/delete.
	id, ok := result.OutputData["id"]
	if !ok {
		resp.Diagnostics.AddError(
			"Create script missing ID",
			"The create script must emit an object whose 'id' field is a non-empty string identifier.",
		)
		return
	}
	idStr := fmt.Sprintf("%v", id)
	if idStr == "" {
		resp.Diagnostics.AddError(
			"Create script returned empty ID",
			"The create script must emit an object whose 'id' field is a non-empty string identifier.",
		)
		return
	}

	data.ID = types.StringValue(idStr)
	if !r.setOutputData(&data, result.OutputData, &resp.Diagnostics) {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Read runs the read_script to refresh the resource's current state. If the
// script reports the resource is gone (empty output or missing id), the resource
// is removed from state so Terraform will plan to recreate it.
func (r *ScriptResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// Read starts from prior state (not plan) since we're refreshing what exists.
	var data ScriptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// During `terraform import`, ImportState seeds the state with only the id; the
	// scripts (which live in config, not state) are still null at this first refresh.
	// With no read_script there is nothing to run, so keep the imported state as-is
	// instead of treating the empty output as "object gone" and dropping it. The
	// following `terraform apply` then reconciles the config (scripts, input_data)
	// into state, after which normal reads work. read_script is Required, so this
	// only ever happens on the import refresh, never in steady-state operation.
	if data.ReadScript.IsNull() || data.ReadScript.IsUnknown() || data.ReadScript.ValueString() == "" {
		tflog.Info(ctx, "Read called with no read_script (import refresh); keeping imported state")
		return
	}

	inputData, ok := r.buildInputData(data, &resp.Diagnostics)
	if !ok {
		return
	}
	// Include the current ID in input so the read script knows what to look up
	if inputData == nil {
		inputData = make(map[string]interface{})
	}
	inputData["id"] = data.ID.ValueString()
	timeout := timeoutFromAttr(data.Timeout)

	tflog.Debug(ctx, "Executing read script", map[string]interface{}{"id": data.ID.ValueString()})
	result, err := r.provider.psManager.Execute("read", data.ReadScript.ValueString(), inputData, timeout)
	if err != nil {
		resp.Diagnostics.AddError("Read script execution failed", err.Error())
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Read script returned an error", result.Error)
		return
	}

	// If OutputData is empty or has no id, the resource no longer exists
	if len(result.OutputData) == 0 {
		tflog.Info(ctx, "Read script returned empty output, removing resource from state")
		resp.State.RemoveResource(ctx)
		return
	}
	if id, ok := result.OutputData["id"]; !ok || fmt.Sprintf("%v", id) == "" {
		tflog.Info(ctx, "Read script returned no id, removing resource from state")
		resp.State.RemoveResource(ctx)
		return
	}

	if !r.setOutputData(&data, result.OutputData, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update runs the update_script for in-place input changes. Updates that change
// only config metadata (scripts, timeout) execute nothing and simply persist the
// new config, carrying the previous outputs forward. The id is computed (never
// in the plan), so it is copied over from prior state before the script runs.
func (r *ScriptResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Start from the planned (new) values.
	var data ScriptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve the existing ID from state
	var state ScriptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ID = state.ID

	// Only input changes require running the update_script against the target
	// system; edits to the scripts themselves (or timeout) are config-only
	// updates. ModifyPlan already forced a replacement for input changes when
	// update_script is absent, so reaching here with unchanged input means
	// there is nothing to execute.
	inputChanged := !data.InputData.Equal(state.InputData) ||
		!data.SensitiveInputData.Equal(state.SensitiveInputData)
	hasUpdateScript := !data.UpdateScript.IsNull() && !data.UpdateScript.IsUnknown() &&
		data.UpdateScript.ValueString() != ""
	if !inputChanged || !hasUpdateScript {
		tflog.Debug(ctx, "Config-only update; not executing update script", map[string]interface{}{"id": data.ID.ValueString()})
		data.OutputData = state.OutputData
		data.SensitiveOutputData = state.SensitiveOutputData
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}

	inputData, ok := r.buildInputData(data, &resp.Diagnostics)
	if !ok {
		return
	}
	if inputData == nil {
		inputData = make(map[string]interface{})
	}
	inputData["id"] = data.ID.ValueString()
	timeout := timeoutFromAttr(data.Timeout)

	tflog.Debug(ctx, "Executing update script", map[string]interface{}{"id": data.ID.ValueString()})
	result, err := r.provider.psManager.Execute("update", data.UpdateScript.ValueString(), inputData, timeout)
	if err != nil {
		resp.Diagnostics.AddError("Update script execution failed", err.Error())
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Update script returned an error", result.Error)
		return
	}

	if !r.setOutputData(&data, result.OutputData, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Delete runs the delete_script to destroy the resource. On success the
// framework drops the resource from state automatically; no state is written.
func (r *ScriptResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ScriptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	inputData, ok := r.buildInputData(data, &resp.Diagnostics)
	if !ok {
		return
	}
	if inputData == nil {
		inputData = make(map[string]interface{})
	}
	inputData["id"] = data.ID.ValueString()
	timeout := timeoutFromAttr(data.Timeout)

	tflog.Debug(ctx, "Executing delete script", map[string]interface{}{"id": data.ID.ValueString()})
	result, err := r.provider.psManager.Execute("delete", data.DeleteScript.ValueString(), inputData, timeout)
	if err != nil {
		resp.Diagnostics.AddError("Delete script execution failed", err.Error())
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Delete script returned an error", result.Error)
		return
	}
}

// ImportState supports `terraform import` by taking the supplied id string and
// writing it to the resource's id attribute. A subsequent Read then populates
// the rest of the state from the read_script.
func (r *ScriptResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// buildInputData merges input_data and sensitive_input_data into the $InputData
// map, adding an error diagnostic (and returning ok=false) on malformed JSON.
func (r *ScriptResource) buildInputData(data ScriptResourceModel, diags *diag.Diagnostics) (map[string]interface{}, bool) {
	inputData, err := mergeInputData(data.InputData, data.SensitiveInputData)
	if err != nil {
		diags.AddError("Invalid input data", err.Error())
		return nil, false
	}
	return inputData, true
}

// setOutputData splits the script's emitted object into output_data and
// sensitive_output_data on the model, adding an error diagnostic (and returning
// false) if the output cannot be encoded.
func (r *ScriptResource) setOutputData(data *ScriptResourceModel, outputData map[string]interface{}, diags *diag.Diagnostics) bool {
	output, sensitiveOutput, err := splitOutputData(outputData)
	if err != nil {
		diags.AddError("Invalid script output", err.Error())
		return false
	}
	data.OutputData = output
	data.SensitiveOutputData = sensitiveOutput
	return true
}
