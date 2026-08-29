package scriptprovider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Compile-time interface assertions for the dynamic data source.
var (
	_ datasource.DataSource              = &dynamicDataSource{}
	_ datasource.DataSourceWithConfigure = &dynamicDataSource{}
)

// newDynamicDataSource returns a constructor closing over one parsed
// data-source definition.
func newDynamicDataSource(def *DataSourceDefinition) func() datasource.DataSource {
	return func() datasource.DataSource {
		return &dynamicDataSource{def: def}
	}
}

type dynamicDataSource struct {
	def      *DataSourceDefinition
	provider *definitionProvider
}

func (d *dynamicDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.def.Name
}

func (d *dynamicDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = d.def.Schema
}

func (d *dynamicDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	p, ok := req.ProviderData.(*definitionProvider)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *definitionProvider, got: %T. This is a bug in the provider.", req.ProviderData),
		)
		return
	}
	d.provider = p
}

// Read runs read.ps1 with $InputData built from the config attributes and maps
// the emitted object onto the computed attributes. An empty emission is not an
// error: every computed attribute becomes null.
func (d *dynamicDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	input, diags := inputDataFromConfig(ctx, d.def.Manifest, req.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Executing data source read script", map[string]interface{}{"data_source": d.def.Name})
	result, err := d.provider.psManager.Execute("read", d.def.ReadScript, input, d.def.Timeout)
	if err != nil {
		resp.Diagnostics.AddError("Read Failed",
			fmt.Sprintf("Failed to execute data source read script: %s", err))
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Read Failed",
			fmt.Sprintf("Data source read script returned an error: %s", result.Error))
		return
	}

	// Seed state from config, then overlay the computed attributes (missing
	// keys become typed nulls inside applyOutputData).
	resp.State.Raw = req.Config.Raw
	resp.Diagnostics.Append(applyOutputData(ctx, d.def.Manifest, result.OutputData, &resp.State)...)
}
