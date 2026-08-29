package scriptprovider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func specOf(t *testing.T, kind manifestKind, attrJSON string) *AttrSpec {
	t.Helper()
	m, err := parseManifest([]byte(`{"version": 1, "attributes": {"a": `+attrJSON+`}}`), kind, "test")
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	return m.Attributes["a"]
}

func TestNativeFromAttrValue(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		spec  *AttrSpec
		value attr.Value
		want  interface{}
	}{
		{"string", specOf(t, manifestResource, `{"type": "string", "required": true}`),
			types.StringValue("hi"), "hi"},
		{"bool", specOf(t, manifestResource, `{"type": "bool", "required": true}`),
			types.BoolValue(true), true},
		{"int", specOf(t, manifestResource, `{"type": "int", "required": true}`),
			types.Int64Value(42), int64(42)},
		{"number", specOf(t, manifestResource, `{"type": "number", "required": true}`),
			types.Float64Value(1.5), 1.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := nativeFromAttrValue(tc.spec, tc.value)
			if err != nil {
				t.Fatalf("nativeFromAttrValue: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v (%T), want %v (%T)", got, got, tc.want, tc.want)
			}
		})
	}

	// json decodes to a real object.
	jsonSpec := specOf(t, manifestResource, `{"type": "json", "optional": true}`)
	got, err := nativeFromAttrValue(jsonSpec, types.StringValue(`{"x": 1}`))
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	obj, ok := got.(map[string]interface{})
	if !ok || obj["x"] != float64(1) {
		t.Errorf("json decoded to %v", got)
	}

	// list of strings.
	listSpec := specOf(t, manifestResource, `{"type": "list", "element_type": "string", "optional": true}`)
	lv, diags := types.ListValue(types.StringType, []attr.Value{types.StringValue("a"), types.StringValue("b")})
	if diags.HasError() {
		t.Fatalf("list build: %v", diags)
	}
	gotList, err := nativeFromAttrValue(listSpec, lv)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	arr := gotList.([]interface{})
	if len(arr) != 2 || arr[0] != "a" {
		t.Errorf("list decoded to %v", arr)
	}

	// map of ints.
	mapSpec := specOf(t, manifestResource, `{"type": "map", "element_type": "int", "optional": true}`)
	mv, diags := types.MapValue(types.Int64Type, map[string]attr.Value{"k": types.Int64Value(7)})
	if diags.HasError() {
		t.Fatalf("map build: %v", diags)
	}
	gotMap, err := nativeFromAttrValue(mapSpec, mv)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if gotMap.(map[string]interface{})["k"] != int64(7) {
		t.Errorf("map decoded to %v", gotMap)
	}
	_ = ctx
}

func TestAttrValueFromNative(t *testing.T) {
	ctx := context.Background()

	strSpec := specOf(t, manifestResource, `{"type": "string", "computed": true}`)
	v, err := attrValueFromNative(ctx, strSpec, "ok")
	if err != nil || v.(types.String).ValueString() != "ok" {
		t.Errorf("string: %v %v", v, err)
	}
	if _, err := attrValueFromNative(ctx, strSpec, 12.0); err == nil {
		t.Error("string from number should error")
	}

	intSpec := specOf(t, manifestResource, `{"type": "int", "computed": true}`)
	v, err = attrValueFromNative(ctx, intSpec, 42.0) // JSON numbers arrive as float64
	if err != nil || v.(types.Int64).ValueInt64() != 42 {
		t.Errorf("int: %v %v", v, err)
	}
	if _, err := attrValueFromNative(ctx, intSpec, 1.5); err == nil {
		t.Error("int from non-integral float should error")
	}

	boolSpec := specOf(t, manifestResource, `{"type": "bool", "computed": true}`)
	v, err = attrValueFromNative(ctx, boolSpec, true)
	if err != nil || !v.(types.Bool).ValueBool() {
		t.Errorf("bool: %v %v", v, err)
	}

	jsonSpec := specOf(t, manifestResource, `{"type": "json", "computed": true}`)
	v, err = attrValueFromNative(ctx, jsonSpec, map[string]interface{}{"a": 1.0})
	if err != nil || !strings.Contains(v.(types.String).ValueString(), `"a":1`) {
		t.Errorf("json: %v %v", v, err)
	}

	listSpec := specOf(t, manifestResource, `{"type": "list", "element_type": "string", "computed": true}`)
	v, err = attrValueFromNative(ctx, listSpec, []interface{}{"x", "y"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if elems := v.(types.List).Elements(); len(elems) != 2 {
		t.Errorf("list: %v", elems)
	}
	if _, err := attrValueFromNative(ctx, listSpec, []interface{}{"x", 3.0}); err == nil {
		t.Error("mixed list should error")
	}

	setSpec := specOf(t, manifestResource, `{"type": "set", "element_type": "int", "computed": true}`)
	v, err = attrValueFromNative(ctx, setSpec, []interface{}{1.0, 2.0})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if elems := v.(types.Set).Elements(); len(elems) != 2 {
		t.Errorf("set: %v", elems)
	}

	mapSpec := specOf(t, manifestResource, `{"type": "map", "element_type": "bool", "computed": true}`)
	v, err = attrValueFromNative(ctx, mapSpec, map[string]interface{}{"on": true})
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if elems := v.(types.Map).Elements(); len(elems) != 1 {
		t.Errorf("map: %v", elems)
	}
}

func TestUnknownAndNullValues(t *testing.T) {
	specs := []*AttrSpec{
		specOf(t, manifestResource, `{"type": "string", "computed": true}`),
		specOf(t, manifestResource, `{"type": "bool", "computed": true}`),
		specOf(t, manifestResource, `{"type": "int", "computed": true}`),
		specOf(t, manifestResource, `{"type": "number", "computed": true}`),
		specOf(t, manifestResource, `{"type": "json", "computed": true}`),
		specOf(t, manifestResource, `{"type": "list", "element_type": "string", "computed": true}`),
		specOf(t, manifestResource, `{"type": "set", "element_type": "number", "computed": true}`),
		specOf(t, manifestResource, `{"type": "map", "element_type": "int", "computed": true}`),
	}
	for _, spec := range specs {
		u := unknownValueFor(spec)
		if !u.IsUnknown() {
			t.Errorf("%s: unknownValueFor not unknown", spec.Type)
		}
		n := nullValueFor(spec)
		if !n.IsNull() {
			t.Errorf("%s: nullValueFor not null", spec.Type)
		}
		if !u.Type(context.Background()).Equal(attrType(spec)) {
			t.Errorf("%s: unknown type mismatch", spec.Type)
		}
	}
}
