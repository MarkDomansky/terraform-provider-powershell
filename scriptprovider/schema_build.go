package scriptprovider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	pschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// stringValidators returns the plan-time validators for a string-backed
// attribute ("json" attributes always get the JSON-object check).
func stringValidators(spec *AttrSpec) []validator.String {
	var out []validator.String
	if spec.Type == attrTypeJSON {
		out = append(out, jsonObjectValidator{})
	}
	for _, v := range spec.Validators {
		values := make([]string, 0, len(v.OneOf))
		for _, raw := range v.OneOf {
			values = append(values, raw.(string))
		}
		out = append(out, stringvalidator.OneOf(values...))
	}
	return out
}

func int64Validators(spec *AttrSpec) []validator.Int64 {
	var out []validator.Int64
	for _, v := range spec.Validators {
		values := make([]int64, 0, len(v.OneOf))
		for _, raw := range v.OneOf {
			values = append(values, int64(raw.(float64)))
		}
		out = append(out, int64validator.OneOf(values...))
	}
	return out
}

func float64Validators(spec *AttrSpec) []validator.Float64 {
	var out []validator.Float64
	for _, v := range spec.Validators {
		values := make([]float64, 0, len(v.OneOf))
		for _, raw := range v.OneOf {
			values = append(values, raw.(float64))
		}
		out = append(out, float64validator.OneOf(values...))
	}
	return out
}

// buildResourceSchema turns a validated resource manifest into a framework
// schema. Framework schemas are plain values, so building them at runtime from
// parsed manifests is fully supported. The automatic "id" attribute mirrors
// the generic script resource: computed, stable across plans via
// UseStateForUnknown.
func buildResourceSchema(m *Manifest) (rschema.Schema, error) {
	attrs := map[string]rschema.Attribute{
		"id": rschema.StringAttribute{
			Description: "Unique identifier emitted by the create script.",
			Computed:    true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			},
		},
	}
	for name, spec := range m.Attributes {
		a, err := buildResourceAttribute(spec)
		if err != nil {
			return rschema.Schema{}, fmt.Errorf("attribute %q: %w", name, err)
		}
		attrs[name] = a
	}
	return rschema.Schema{
		Description: m.Description,
		Attributes:  attrs,
	}, nil
}

// buildResourceAttribute builds one resource attribute from its spec.
func buildResourceAttribute(spec *AttrSpec) (rschema.Attribute, error) {
	replace := spec.RequiresReplace
	switch spec.Type {
	case attrTypeString, attrTypeJSON:
		a := rschema.StringAttribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
			Validators:  stringValidators(spec),
		}
		if replace {
			a.PlanModifiers = []planmodifier.String{stringplanmodifier.RequiresReplace()}
		}
		return a, nil
	case attrTypeBool:
		a := rschema.BoolAttribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
		}
		if replace {
			a.PlanModifiers = []planmodifier.Bool{boolplanmodifier.RequiresReplace()}
		}
		return a, nil
	case attrTypeInt:
		a := rschema.Int64Attribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
			Validators:  int64Validators(spec),
		}
		if replace {
			a.PlanModifiers = []planmodifier.Int64{int64planmodifier.RequiresReplace()}
		}
		return a, nil
	case attrTypeNumber:
		a := rschema.Float64Attribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
			Validators:  float64Validators(spec),
		}
		if replace {
			a.PlanModifiers = []planmodifier.Float64{float64planmodifier.RequiresReplace()}
		}
		return a, nil
	case attrTypeList:
		a := rschema.ListAttribute{
			Description: spec.Description,
			ElementType: elementAttrType(spec.ElementType),
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
		}
		if replace {
			a.PlanModifiers = []planmodifier.List{listplanmodifier.RequiresReplace()}
		}
		return a, nil
	case attrTypeSet:
		a := rschema.SetAttribute{
			Description: spec.Description,
			ElementType: elementAttrType(spec.ElementType),
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
		}
		if replace {
			a.PlanModifiers = []planmodifier.Set{setplanmodifier.RequiresReplace()}
		}
		return a, nil
	case attrTypeMap:
		a := rschema.MapAttribute{
			Description: spec.Description,
			ElementType: elementAttrType(spec.ElementType),
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
		}
		if replace {
			a.PlanModifiers = []planmodifier.Map{mapplanmodifier.RequiresReplace()}
		}
		return a, nil
	default:
		return nil, fmt.Errorf("unsupported type %q", spec.Type)
	}
}

// buildDataSourceSchema turns a validated data-source manifest into a
// framework schema.
func buildDataSourceSchema(m *Manifest) (dschema.Schema, error) {
	attrs := make(map[string]dschema.Attribute, len(m.Attributes))
	for name, spec := range m.Attributes {
		a, err := buildDataSourceAttribute(spec)
		if err != nil {
			return dschema.Schema{}, fmt.Errorf("attribute %q: %w", name, err)
		}
		attrs[name] = a
	}
	return dschema.Schema{
		Description: m.Description,
		Attributes:  attrs,
	}, nil
}

func buildDataSourceAttribute(spec *AttrSpec) (dschema.Attribute, error) {
	switch spec.Type {
	case attrTypeString, attrTypeJSON:
		return dschema.StringAttribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
			Validators:  stringValidators(spec),
		}, nil
	case attrTypeBool:
		return dschema.BoolAttribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
		}, nil
	case attrTypeInt:
		return dschema.Int64Attribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
			Validators:  int64Validators(spec),
		}, nil
	case attrTypeNumber:
		return dschema.Float64Attribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
			Validators:  float64Validators(spec),
		}, nil
	case attrTypeList:
		return dschema.ListAttribute{
			Description: spec.Description,
			ElementType: elementAttrType(spec.ElementType),
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
		}, nil
	case attrTypeSet:
		return dschema.SetAttribute{
			Description: spec.Description,
			ElementType: elementAttrType(spec.ElementType),
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
		}, nil
	case attrTypeMap:
		return dschema.MapAttribute{
			Description: spec.Description,
			ElementType: elementAttrType(spec.ElementType),
			Required:    spec.Required,
			Optional:    spec.Optional,
			Computed:    spec.Computed,
			Sensitive:   spec.Sensitive,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported type %q", spec.Type)
	}
}

// buildProviderAttributes builds the custom provider attributes declared by a
// provider manifest. The caller merges them over builtinProviderAttributes
// after checking for collisions.
func buildProviderAttributes(m *Manifest) (map[string]pschema.Attribute, error) {
	attrs := make(map[string]pschema.Attribute, len(m.Attributes))
	for name, spec := range m.Attributes {
		a, err := buildProviderAttribute(spec)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", name, err)
		}
		attrs[name] = a
	}
	return attrs, nil
}

func buildProviderAttribute(spec *AttrSpec) (pschema.Attribute, error) {
	switch spec.Type {
	case attrTypeString, attrTypeJSON:
		return pschema.StringAttribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Sensitive:   spec.Sensitive,
			Validators:  stringValidators(spec),
		}, nil
	case attrTypeBool:
		return pschema.BoolAttribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Sensitive:   spec.Sensitive,
		}, nil
	case attrTypeInt:
		return pschema.Int64Attribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Sensitive:   spec.Sensitive,
			Validators:  int64Validators(spec),
		}, nil
	case attrTypeNumber:
		return pschema.Float64Attribute{
			Description: spec.Description,
			Required:    spec.Required,
			Optional:    spec.Optional,
			Sensitive:   spec.Sensitive,
			Validators:  float64Validators(spec),
		}, nil
	case attrTypeList:
		return pschema.ListAttribute{
			Description: spec.Description,
			ElementType: elementAttrType(spec.ElementType),
			Required:    spec.Required,
			Optional:    spec.Optional,
			Sensitive:   spec.Sensitive,
		}, nil
	case attrTypeSet:
		return pschema.SetAttribute{
			Description: spec.Description,
			ElementType: elementAttrType(spec.ElementType),
			Required:    spec.Required,
			Optional:    spec.Optional,
			Sensitive:   spec.Sensitive,
		}, nil
	case attrTypeMap:
		return pschema.MapAttribute{
			Description: spec.Description,
			ElementType: elementAttrType(spec.ElementType),
			Required:    spec.Required,
			Optional:    spec.Optional,
			Sensitive:   spec.Sensitive,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported type %q", spec.Type)
	}
}
