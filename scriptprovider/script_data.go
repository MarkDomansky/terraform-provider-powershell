package scriptprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// sensitiveOutputKey is the reserved top-level key in a script's emitted object.
// Its value is extracted into the sensitive_output_data attribute and removed
// from output_data, so secrets a script returns never show in plan output.
const sensitiveOutputKey = "sensitive"

// jsonObjectValidator validates at plan time that a string attribute holds a
// JSON object (as produced by jsonencode({...})). Without it, malformed JSON
// would only surface at apply time — or worse, scripts would silently receive
// an empty $InputData.
type jsonObjectValidator struct{}

var _ validator.String = jsonObjectValidator{}

func (v jsonObjectValidator) Description(_ context.Context) string {
	return "value must be a JSON object string (use jsonencode({...}))"
}

func (v jsonObjectValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v jsonObjectValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(req.ConfigValue.ValueString()), &obj); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid JSON Object",
			fmt.Sprintf("The value must be a JSON object string (use jsonencode({...})): %s", err),
		)
	}
}

// parseJSONObject decodes a JSON-object attribute into a map. Null/unknown/empty
// values yield nil without error; malformed JSON is a real error the caller must
// surface as a diagnostic (never silently swallowed).
func parseJSONObject(v types.String, attrName string) (map[string]interface{}, error) {
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return nil, nil
	}
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(v.ValueString()), &data); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object string (use jsonencode({...})): %w", attrName, err)
	}
	return data, nil
}

// mergeInputData combines input_data and sensitive_input_data into the single
// map bound to scripts as $InputData. On key collision the sensitive value wins,
// so secrets can override placeholder values without renaming keys.
func mergeInputData(input, sensitiveInput types.String) (map[string]interface{}, error) {
	data, err := parseJSONObject(input, "input_data")
	if err != nil {
		return nil, err
	}
	sens, err := parseJSONObject(sensitiveInput, "sensitive_input_data")
	if err != nil {
		return nil, err
	}
	if len(sens) == 0 {
		return data, nil
	}
	if data == nil {
		data = make(map[string]interface{}, len(sens))
	}
	for k, v := range sens {
		data[k] = v
	}
	return data, nil
}

// splitOutputData converts a script's emitted object into the output_data and
// sensitive_output_data state values. The reserved "sensitive" top-level key is
// extracted into the sensitive JSON string; everything else becomes output_data.
// The input map is modified (the sensitive key is removed).
func splitOutputData(outputData map[string]interface{}) (output, sensitiveOutput types.String, err error) {
	sensitiveJSON := "{}"
	if raw, ok := outputData[sensitiveOutputKey]; ok {
		delete(outputData, sensitiveOutputKey)
		b, merr := json.Marshal(raw)
		if merr != nil {
			return types.StringNull(), types.StringNull(), fmt.Errorf("failed to encode the script's 'sensitive' output as JSON: %w", merr)
		}
		sensitiveJSON = string(b)
	}

	outputJSON := "{}"
	if len(outputData) > 0 {
		b, merr := json.Marshal(outputData)
		if merr != nil {
			return types.StringNull(), types.StringNull(), fmt.Errorf("failed to encode the script's output as JSON: %w", merr)
		}
		outputJSON = string(b)
	}
	return types.StringValue(outputJSON), types.StringValue(sensitiveJSON), nil
}

// timeoutFromAttr returns the configured timeout, or zero so PSManager applies
// its default.
func timeoutFromAttr(timeout types.Int64) time.Duration {
	if timeout.IsNull() || timeout.IsUnknown() {
		return 0
	}
	return time.Duration(timeout.ValueInt64()) * time.Second
}
