package scriptprovider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// attrGetter is the subset of tfsdk.Config / tfsdk.Plan / tfsdk.State used to
// read one attribute at a time. Reading attribute-wise (instead of decoding
// into a model struct) is what lets a single Go type serve any manifest.
type attrGetter interface {
	GetAttribute(ctx context.Context, p path.Path, target interface{}) diag.Diagnostics
}

// attrSetter is the subset of *tfsdk.State used to write one attribute.
type attrSetter interface {
	SetAttribute(ctx context.Context, p path.Path, val interface{}) diag.Diagnostics
}

// elementAttrType maps a manifest element type to the framework type of a
// collection element.
func elementAttrType(elementType string) attr.Type {
	switch elementType {
	case attrTypeBool:
		return types.BoolType
	case attrTypeInt:
		return types.Int64Type
	case attrTypeNumber:
		return types.Float64Type
	default:
		return types.StringType
	}
}

// attrType maps an AttrSpec to its framework value type.
func attrType(spec *AttrSpec) attr.Type {
	switch spec.Type {
	case attrTypeBool:
		return types.BoolType
	case attrTypeInt:
		return types.Int64Type
	case attrTypeNumber:
		return types.Float64Type
	case attrTypeList:
		return types.ListType{ElemType: elementAttrType(spec.ElementType)}
	case attrTypeSet:
		return types.SetType{ElemType: elementAttrType(spec.ElementType)}
	case attrTypeMap:
		return types.MapType{ElemType: elementAttrType(spec.ElementType)}
	default: // string, json
		return types.StringType
	}
}

// unknownValueFor returns the typed Unknown for an attribute, used at plan time
// when a script will recompute the attribute's value during apply.
func unknownValueFor(spec *AttrSpec) attr.Value {
	switch spec.Type {
	case attrTypeBool:
		return types.BoolUnknown()
	case attrTypeInt:
		return types.Int64Unknown()
	case attrTypeNumber:
		return types.Float64Unknown()
	case attrTypeList:
		return types.ListUnknown(elementAttrType(spec.ElementType))
	case attrTypeSet:
		return types.SetUnknown(elementAttrType(spec.ElementType))
	case attrTypeMap:
		return types.MapUnknown(elementAttrType(spec.ElementType))
	default:
		return types.StringUnknown()
	}
}

// nullValueFor returns the typed Null for an attribute, used when a script's
// emitted object omits a computed attribute.
func nullValueFor(spec *AttrSpec) attr.Value {
	switch spec.Type {
	case attrTypeBool:
		return types.BoolNull()
	case attrTypeInt:
		return types.Int64Null()
	case attrTypeNumber:
		return types.Float64Null()
	case attrTypeList:
		return types.ListNull(elementAttrType(spec.ElementType))
	case attrTypeSet:
		return types.SetNull(elementAttrType(spec.ElementType))
	case attrTypeMap:
		return types.MapNull(elementAttrType(spec.ElementType))
	default:
		return types.StringNull()
	}
}

// primitiveToNative converts a primitive framework value to the native Go value
// carried into $InputData. Null and unknown yield nil.
func primitiveToNative(v attr.Value) interface{} {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	switch tv := v.(type) {
	case types.String:
		return tv.ValueString()
	case types.Bool:
		return tv.ValueBool()
	case types.Int64:
		return tv.ValueInt64()
	case types.Float64:
		return tv.ValueFloat64()
	default:
		return nil
	}
}

// nativeFromAttrValue converts a known framework value to the native
// representation placed into $InputData, honoring the manifest type ("json"
// strings are decoded into real objects so scripts receive structured data).
func nativeFromAttrValue(spec *AttrSpec, v attr.Value) (interface{}, error) {
	switch spec.Type {
	case attrTypeString:
		return v.(types.String).ValueString(), nil
	case attrTypeJSON:
		var decoded map[string]interface{}
		raw := v.(types.String).ValueString()
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return nil, fmt.Errorf("value is not a JSON object: %w", err)
		}
		return decoded, nil
	case attrTypeBool:
		return v.(types.Bool).ValueBool(), nil
	case attrTypeInt:
		return v.(types.Int64).ValueInt64(), nil
	case attrTypeNumber:
		return v.(types.Float64).ValueFloat64(), nil
	case attrTypeList:
		return elementsToNative(v.(types.List).Elements()), nil
	case attrTypeSet:
		return elementsToNative(v.(types.Set).Elements()), nil
	case attrTypeMap:
		elems := v.(types.Map).Elements()
		out := make(map[string]interface{}, len(elems))
		for k, el := range elems {
			out[k] = primitiveToNative(el)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported type %q", spec.Type)
	}
}

// elementsToNative converts a slice of primitive framework values.
func elementsToNative(elems []attr.Value) []interface{} {
	out := make([]interface{}, 0, len(elems))
	for _, el := range elems {
		out = append(out, primitiveToNative(el))
	}
	return out
}

// attrValueFromNative converts a value emitted by a script into the typed
// framework value for a computed attribute, enforcing the declared type.
// JSON numbers arrive as float64; integral float64s are accepted for int.
func attrValueFromNative(ctx context.Context, spec *AttrSpec, v interface{}) (attr.Value, error) {
	mismatch := func(expected string) (attr.Value, error) {
		return nil, fmt.Errorf("expected %s, got %T", expected, v)
	}
	switch spec.Type {
	case attrTypeString:
		s, ok := v.(string)
		if !ok {
			return mismatch("a string")
		}
		return types.StringValue(s), nil
	case attrTypeJSON:
		obj, ok := v.(map[string]interface{})
		if !ok {
			return mismatch("a JSON object")
		}
		encoded, err := json.Marshal(obj)
		if err != nil {
			return nil, fmt.Errorf("could not re-encode emitted object: %w", err)
		}
		return types.StringValue(string(encoded)), nil
	case attrTypeBool:
		b, ok := v.(bool)
		if !ok {
			return mismatch("a bool")
		}
		return types.BoolValue(b), nil
	case attrTypeInt:
		switch n := v.(type) {
		case float64:
			if n != float64(int64(n)) {
				return nil, fmt.Errorf("expected an integer, got %v", n)
			}
			return types.Int64Value(int64(n)), nil
		case int64:
			return types.Int64Value(n), nil
		default:
			return mismatch("an integer")
		}
	case attrTypeNumber:
		switch n := v.(type) {
		case float64:
			return types.Float64Value(n), nil
		case int64:
			return types.Float64Value(float64(n)), nil
		default:
			return mismatch("a number")
		}
	case attrTypeList, attrTypeSet:
		arr, ok := v.([]interface{})
		if !ok {
			return mismatch("an array")
		}
		elemType := elementAttrType(spec.ElementType)
		elems, err := nativeElements(ctx, spec.ElementType, arr)
		if err != nil {
			return nil, err
		}
		if spec.Type == attrTypeList {
			lv, diags := types.ListValue(elemType, elems)
			if diags.HasError() {
				return nil, fmt.Errorf("could not build list value: %v", diags)
			}
			return lv, nil
		}
		sv, diags := types.SetValue(elemType, elems)
		if diags.HasError() {
			return nil, fmt.Errorf("could not build set value: %v", diags)
		}
		return sv, nil
	case attrTypeMap:
		obj, ok := v.(map[string]interface{})
		if !ok {
			return mismatch("an object")
		}
		elemType := elementAttrType(spec.ElementType)
		elems := make(map[string]attr.Value, len(obj))
		for k, el := range obj {
			ev, err := nativeElement(spec.ElementType, el)
			if err != nil {
				return nil, fmt.Errorf("element %q: %w", k, err)
			}
			elems[k] = ev
		}
		mv, diags := types.MapValue(elemType, elems)
		if diags.HasError() {
			return nil, fmt.Errorf("could not build map value: %v", diags)
		}
		return mv, nil
	default:
		return nil, fmt.Errorf("unsupported type %q", spec.Type)
	}
}

// nativeElements converts emitted array elements to framework values.
func nativeElements(_ context.Context, elementType string, arr []interface{}) ([]attr.Value, error) {
	out := make([]attr.Value, 0, len(arr))
	for i, el := range arr {
		ev, err := nativeElement(elementType, el)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		out = append(out, ev)
	}
	return out, nil
}

// nativeElement converts one emitted collection element.
func nativeElement(elementType string, v interface{}) (attr.Value, error) {
	switch elementType {
	case attrTypeBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("expected a bool, got %T", v)
		}
		return types.BoolValue(b), nil
	case attrTypeInt:
		f, ok := v.(float64)
		if !ok || f != float64(int64(f)) {
			return nil, fmt.Errorf("expected an integer, got %v", v)
		}
		return types.Int64Value(int64(f)), nil
	case attrTypeNumber:
		f, ok := v.(float64)
		if !ok {
			return nil, fmt.Errorf("expected a number, got %T", v)
		}
		return types.Float64Value(f), nil
	default:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("expected a string, got %T", v)
		}
		return types.StringValue(s), nil
	}
}

// inputDataFromConfig builds the $InputData map from a manifest's config
// (non-computed) attributes read from src (a Plan, Config, or State). Null and
// unknown attributes are omitted, unless the manifest declares a default, in
// which case the default is injected (state is not touched — the default is
// visible to the script only). The diagnostics name the failing attribute.
func inputDataFromConfig(ctx context.Context, m *Manifest, src attrGetter) (map[string]interface{}, diag.Diagnostics) {
	var diags diag.Diagnostics
	input := make(map[string]interface{})
	for _, name := range m.configNames() {
		spec := m.Attributes[name]
		target := nullValueFor(spec)
		// GetAttribute needs a pointer to a concrete framework value type.
		val, d := getTypedAttribute(ctx, src, path.Root(name), target)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
		if val.IsNull() || val.IsUnknown() {
			if spec.defaultValue != nil {
				input[name] = spec.defaultValue
			}
			continue
		}
		native, err := nativeFromAttrValue(spec, val)
		if err != nil {
			diags.AddAttributeError(path.Root(name), "Invalid Attribute Value",
				fmt.Sprintf("Could not convert %q for script input: %s", name, err))
			return nil, diags
		}
		input[name] = native
	}
	return input, diags
}

// getTypedAttribute reads one attribute as its concrete framework value type.
func getTypedAttribute(ctx context.Context, src attrGetter, p path.Path, proto attr.Value) (attr.Value, diag.Diagnostics) {
	switch proto.(type) {
	case types.Bool:
		var v types.Bool
		d := src.GetAttribute(ctx, p, &v)
		return v, d
	case types.Int64:
		var v types.Int64
		d := src.GetAttribute(ctx, p, &v)
		return v, d
	case types.Float64:
		var v types.Float64
		d := src.GetAttribute(ctx, p, &v)
		return v, d
	case types.List:
		var v types.List
		d := src.GetAttribute(ctx, p, &v)
		return v, d
	case types.Set:
		var v types.Set
		d := src.GetAttribute(ctx, p, &v)
		return v, d
	case types.Map:
		var v types.Map
		d := src.GetAttribute(ctx, p, &v)
		return v, d
	default:
		var v types.String
		d := src.GetAttribute(ctx, p, &v)
		return v, d
	}
}

// applyOutputData writes a script's emitted object onto the computed attributes
// of dst. Missing computed keys become typed nulls (with a warning in TF_LOG
// output, since that usually means a typo in the script); emitted keys that
// name config attributes are ignored (config always comes from the plan);
// entirely undeclared keys are ignored with a warning. A type mismatch on a
// declared computed attribute is an error diagnostic.
func applyOutputData(ctx context.Context, m *Manifest, out map[string]interface{}, dst attrSetter) diag.Diagnostics {
	var diags diag.Diagnostics

	for _, name := range m.computedNames() {
		spec := m.Attributes[name]
		raw, present := out[name]
		if !present || raw == nil {
			tflog.Warn(ctx, "Script output missing computed attribute; setting it to null", map[string]interface{}{
				"attribute": name,
			})
			diags.Append(dst.SetAttribute(ctx, path.Root(name), nullValueFor(spec))...)
			continue
		}
		val, err := attrValueFromNative(ctx, spec, raw)
		if err != nil {
			diags.AddAttributeError(path.Root(name), "Invalid Script Output",
				fmt.Sprintf("The script emitted an invalid value for computed attribute %q: %s", name, err))
			continue
		}
		diags.Append(dst.SetAttribute(ctx, path.Root(name), val)...)
	}

	// Surface unexpected keys so typos are discoverable via TF_LOG.
	for key := range out {
		if key == "id" {
			continue
		}
		if spec, declared := m.Attributes[key]; declared {
			if !spec.Computed {
				tflog.Debug(ctx, "Script echoed a config attribute; ignored (config comes from the plan)", map[string]interface{}{
					"attribute": key,
				})
			}
			continue
		}
		tflog.Warn(ctx, "Script emitted a key that is not a declared attribute; ignored", map[string]interface{}{
			"key": key,
		})
	}

	return diags
}
