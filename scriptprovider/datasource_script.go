package scriptprovider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Compile-time assertion that ScriptDataSource implements the framework's
// DataSource contract.
var _ datasource.DataSourceWithConfigure = &ScriptDataSource{}

// ScriptDataSource implements the powershell_script data source: a read-only
// script executed during refresh, analogous to the hashicorp/external data
// source. It runs in the same persistent PowerShell process (and remote
// session, if any) as every resource, so it can use provider-level globals.
type ScriptDataSource struct {
	provider *PowerShellProvider
}

// ScriptDataSourceModel maps the data block's HCL attributes onto Go fields.
type ScriptDataSourceModel struct {
	Script              types.String `tfsdk:"script"`
	InputData           types.String `tfsdk:"input_data"`
	SensitiveInputData  types.String `tfsdk:"sensitive_input_data"`
	OutputData          types.String `tfsdk:"output_data"`
	SensitiveOutputData types.String `tfsdk:"sensitive_output_data"`
	Timeout             types.Int64  `tfsdk:"timeout"`
}

// NewScriptDataSource is the constructor Terraform calls (via the provider's
// DataSources list) to create a fresh data source instance.
func NewScriptDataSource() datasource.DataSource {
	return &ScriptDataSource{}
}

// Metadata sets the data source's type name by combining the provider prefix
// with "_script", yielding "powershell_script".
func (d *ScriptDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_script"
}

// Schema declares the attributes of the powershell_script data source.
func (d *ScriptDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads data via a PowerShell script executed during refresh. " +
			"The script receives input via the $InputData parameter and returns its result by emitting a " +
			"single object to the output stream (or nothing, yielding empty output_data). " +
			"It must not change any system state.",
		Attributes: map[string]schema.Attribute{
			"script": schema.StringAttribute{
				Description: "PowerShell script executed to read data. " +
					"Must emit at most a single object to the output stream.",
				Required: true,
			},
			"input_data": schema.StringAttribute{
				Description: "JSON string of input data passed to the script as the $InputData parameter (a hashtable). " +
					"Use jsonencode() to construct this value. Put secrets in sensitive_input_data instead.",
				Optional: true,
				Validators: []validator.String{
					jsonObjectValidator{},
				},
			},
			"sensitive_input_data": schema.StringAttribute{
				Description: "JSON string of sensitive input data, merged into $InputData after input_data " +
					"(on key collision the sensitive value wins). Redacted from plan output.",
				Optional:  true,
				Sensitive: true,
				Validators: []validator.String{
					jsonObjectValidator{},
				},
			},
			"output_data": schema.StringAttribute{
				Description: "JSON string of the object the script emitted to the output stream, excluding the " +
					"reserved 'sensitive' key. Use jsondecode() to extract values.",
				Computed: true,
			},
			"sensitive_output_data": schema.StringAttribute{
				Description: "JSON string of the value under the reserved 'sensitive' key of the object the script " +
					"emitted (\"{}\" when the key is absent). Redacted from plan output.",
				Computed:  true,
				Sensitive: true,
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

// Configure receives the configured provider (set as resp.DataSourceData in the
// provider's Configure) and stores it so Read can use its psManager.
func (d *ScriptDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// ProviderData is nil during early framework calls (e.g. validation) before
	// the provider has been configured; nothing to wire up yet.
	if req.ProviderData == nil {
		return
	}
	p, ok := req.ProviderData.(*PowerShellProvider)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *PowerShellProvider, got: %T", req.ProviderData),
		)
		return
	}
	d.provider = p
}

// Read executes the script and records its output. Unlike the resource's Read,
// empty output is not "resource gone" — it simply yields empty output_data.
func (d *ScriptDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ScriptDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	inputData, err := mergeInputData(data.InputData, data.SensitiveInputData)
	if err != nil {
		resp.Diagnostics.AddError("Invalid input data", err.Error())
		return
	}
	timeout := timeoutFromAttr(data.Timeout)

	tflog.Debug(ctx, "Executing data source script")
	result, execErr := d.provider.psManager.Execute("read", data.Script.ValueString(), inputData, timeout)
	if execErr != nil {
		resp.Diagnostics.AddError("Data source script execution failed", execErr.Error())
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Data source script returned an error", result.Error)
		return
	}

	output, sensitiveOutput, err := splitOutputData(result.OutputData)
	if err != nil {
		resp.Diagnostics.AddError("Invalid script output", err.Error())
		return
	}
	data.OutputData = output
	data.SensitiveOutputData = sensitiveOutput

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
