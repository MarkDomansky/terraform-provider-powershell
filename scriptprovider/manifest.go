package scriptprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

// manifestKind selects the validation rules for a manifest file: the three
// contexts share one format but allow different attribute settings.
type manifestKind int

const (
	manifestResource manifestKind = iota
	manifestDataSource
	manifestProvider
)

func (k manifestKind) String() string {
	switch k {
	case manifestResource:
		return "resource"
	case manifestDataSource:
		return "data source"
	default:
		return "provider"
	}
}

// Attribute types accepted in v1 manifests. "json" is a string attribute
// carrying a JSON object: validated as JSON at plan time, decoded to a real
// object in $InputData, and re-encoded to a string when emitted as output.
const (
	attrTypeString = "string"
	attrTypeBool   = "bool"
	attrTypeInt    = "int"
	attrTypeNumber = "number"
	attrTypeList   = "list"
	attrTypeSet    = "set"
	attrTypeMap    = "map"
	attrTypeJSON   = "json"
)

// attrNamePattern is the shape required of attribute names and of
// resource/data-source directory names.
var attrNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Manifest is a parsed resource.tfps.json, datasource.tfps.json, or
// provider.tfps.json. One format serves all three; parseManifest enforces the
// per-context restrictions.
type Manifest struct {
	// Schema is the editor's JSON Schema pointer (the "$schema" key). It
	// carries no provider semantics and is never read after decoding; it is
	// declared only so that strict decoding accepts it. See
	// https://json.schemastore.org/tfpowershell-resource.json and siblings.
	Schema string `json:"$schema"`

	Version        int                  `json:"version"`
	Description    string               `json:"description"`
	TimeoutSeconds int64                `json:"timeout_seconds"`
	Attributes     map[string]*AttrSpec `json:"attributes"`
}

// AttrSpec is one attribute declaration inside a manifest.
type AttrSpec struct {
	Type            string          `json:"type"`
	ElementType     string          `json:"element_type"`
	Required        bool            `json:"required"`
	Optional        bool            `json:"optional"`
	Computed        bool            `json:"computed"`
	Sensitive       bool            `json:"sensitive"`
	Description     string          `json:"description"`
	RequiresReplace bool            `json:"requires_replace"`
	Default         json.RawMessage `json:"default"`
	Validators      []ValidatorSpec `json:"validators"`
	Env             string          `json:"env"`

	// defaultValue is the decoded Default, populated during validation so the
	// conversion layer does not re-parse it on every operation.
	defaultValue interface{}
}

// ValidatorSpec is one entry of an attribute's "validators" array. Each entry
// is an object with exactly one recognized key; unknown keys are load errors,
// which keeps the format forward-extensible without silent misbehavior.
type ValidatorSpec struct {
	OneOf []interface{} `json:"one_of"`
}

// configNames returns the manifest's non-computed attribute names, sorted for
// deterministic iteration.
func (m *Manifest) configNames() []string {
	var names []string
	for name, spec := range m.Attributes {
		if !spec.Computed {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// computedNames returns the manifest's computed attribute names, sorted.
func (m *Manifest) computedNames() []string {
	var names []string
	for name, spec := range m.Attributes {
		if spec.Computed {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// parseManifest decodes and validates one manifest. Parsing is strict: unknown
// top-level, per-attribute, or per-validator keys are errors, so a manifest
// written for a future engine can never be silently half-applied by an old one.
// The one exception is "$schema", which is decoded into Manifest.Schema and
// ignored. source names the file in error messages (e.g.
// "provider/resources/mailbox/resource.tfps.json").
func parseManifest(data []byte, kind manifestKind, source string) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: invalid manifest: %w", source, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%s: invalid manifest: trailing data after the JSON object", source)
	}
	if err := m.validate(kind, source); err != nil {
		return nil, err
	}
	return &m, nil
}

// validate enforces the manifest rules for the given context. It also decodes
// attribute defaults into spec.defaultValue.
func (m *Manifest) validate(kind manifestKind, source string) error {
	fail := func(format string, args ...interface{}) error {
		return fmt.Errorf("%s: %s", source, fmt.Sprintf(format, args...))
	}

	if m.Version != 1 {
		return fail(`"version" must be 1 (got %d); this engine only understands manifest version 1`, m.Version)
	}
	if m.TimeoutSeconds < 0 {
		return fail(`"timeout_seconds" must be positive (got %d)`, m.TimeoutSeconds)
	}
	if kind == manifestProvider && m.TimeoutSeconds != 0 {
		return fail(`"timeout_seconds" is not allowed in a provider manifest; use the built-in "timeout" provider attribute`)
	}
	if len(m.Attributes) == 0 {
		return fail(`"attributes" must declare at least one attribute`)
	}

	for name, spec := range m.Attributes {
		if spec == nil {
			return fail("attribute %q: declaration must be an object", name)
		}
		if err := spec.validate(kind, name); err != nil {
			return fail("%s", err)
		}
	}

	if kind == manifestResource {
		if _, declared := m.Attributes["id"]; declared {
			return fail(`attribute "id" is reserved: every resource automatically gets a computed "id" populated from the create script's emitted id`)
		}
	}
	return nil
}

// validate checks a single attribute declaration.
func (s *AttrSpec) validate(kind manifestKind, name string) error {
	fail := func(format string, args ...interface{}) error {
		return fmt.Errorf("attribute %q: %s", name, fmt.Sprintf(format, args...))
	}

	if !attrNamePattern.MatchString(name) {
		return fail("name must match %s", attrNamePattern.String())
	}

	switch s.Type {
	case attrTypeString, attrTypeBool, attrTypeInt, attrTypeNumber, attrTypeJSON:
		if s.ElementType != "" {
			return fail(`"element_type" only applies to list, set, and map attributes`)
		}
	case attrTypeList, attrTypeSet, attrTypeMap:
		switch s.ElementType {
		case attrTypeString, attrTypeBool, attrTypeInt, attrTypeNumber:
		case "":
			return fail(`"element_type" is required for %s attributes (one of string, bool, int, number)`, s.Type)
		default:
			return fail(`"element_type" must be one of string, bool, int, number (got %q)`, s.ElementType)
		}
	case "":
		return fail(`"type" is required`)
	default:
		return fail(`unknown type %q (expected string, bool, int, number, list, set, map, or json)`, s.Type)
	}

	// Exactly one behavior flag. v1 deliberately has no Optional+Computed
	// combination: it would drag in the framework's default-value machinery.
	behaviors := 0
	for _, b := range []bool{s.Required, s.Optional, s.Computed} {
		if b {
			behaviors++
		}
	}
	if behaviors != 1 {
		return fail(`exactly one of "required", "optional", or "computed" must be true`)
	}

	if kind == manifestProvider && s.Computed {
		return fail("provider attributes cannot be computed")
	}
	if s.RequiresReplace {
		if kind != manifestResource {
			return fail(`"requires_replace" only applies to resource attributes`)
		}
		if s.Computed {
			return fail(`"requires_replace" cannot be set on a computed attribute`)
		}
	}
	if s.Env != "" {
		if kind != manifestProvider {
			return fail(`"env" only applies to provider attributes`)
		}
		if s.Type != attrTypeString {
			return fail(`"env" only applies to string attributes`)
		}
		if s.Computed || s.Required {
			return fail(`"env" only applies to optional attributes`)
		}
	}

	if len(s.Default) > 0 && !bytes.Equal(s.Default, []byte("null")) {
		if !s.Optional {
			return fail(`"default" only applies to optional attributes`)
		}
		v, err := s.decodeDefault()
		if err != nil {
			return fail(`"default" %s`, err)
		}
		s.defaultValue = v
	}

	for i, val := range s.Validators {
		if len(val.OneOf) == 0 {
			return fail(`validators[%d]: must be an object with a non-empty "one_of" array`, i)
		}
		switch s.Type {
		case attrTypeString:
			for _, v := range val.OneOf {
				if _, ok := v.(string); !ok {
					return fail(`validators[%d]: "one_of" values must be strings for a string attribute`, i)
				}
			}
		case attrTypeInt, attrTypeNumber:
			for _, v := range val.OneOf {
				if _, ok := v.(float64); !ok {
					return fail(`validators[%d]: "one_of" values must be numbers for a %s attribute`, i, s.Type)
				}
			}
		default:
			return fail(`validators[%d]: "one_of" only applies to string, int, and number attributes`, i)
		}
		if s.Computed {
			return fail(`validators[%d]: validators cannot be set on a computed attribute`, i)
		}
	}

	return nil
}

// decodeDefault decodes the raw default into the native value the conversion
// layer injects into $InputData, checking it against the declared type.
func (s *AttrSpec) decodeDefault() (interface{}, error) {
	var v interface{}
	if err := json.Unmarshal(s.Default, &v); err != nil {
		return nil, fmt.Errorf("is not valid JSON: %w", err)
	}
	mismatch := func() (interface{}, error) {
		return nil, fmt.Errorf("value %s does not match the declared type %q", string(s.Default), s.Type)
	}
	switch s.Type {
	case attrTypeString:
		if _, ok := v.(string); !ok {
			return mismatch()
		}
	case attrTypeBool:
		if _, ok := v.(bool); !ok {
			return mismatch()
		}
	case attrTypeInt:
		f, ok := v.(float64)
		if !ok || f != float64(int64(f)) {
			return mismatch()
		}
	case attrTypeNumber:
		if _, ok := v.(float64); !ok {
			return mismatch()
		}
	case attrTypeJSON:
		if _, ok := v.(map[string]interface{}); !ok {
			return nil, fmt.Errorf("value for a json attribute must be a JSON object")
		}
	case attrTypeList, attrTypeSet:
		arr, ok := v.([]interface{})
		if !ok {
			return mismatch()
		}
		for _, el := range arr {
			if !elementMatches(s.ElementType, el) {
				return nil, fmt.Errorf("element %v does not match element_type %q", el, s.ElementType)
			}
		}
	case attrTypeMap:
		obj, ok := v.(map[string]interface{})
		if !ok {
			return mismatch()
		}
		for k, el := range obj {
			if !elementMatches(s.ElementType, el) {
				return nil, fmt.Errorf("element %q=%v does not match element_type %q", k, el, s.ElementType)
			}
		}
	}
	return v, nil
}

// elementMatches reports whether a decoded JSON value matches a collection
// element type.
func elementMatches(elementType string, v interface{}) bool {
	switch elementType {
	case attrTypeString:
		_, ok := v.(string)
		return ok
	case attrTypeBool:
		_, ok := v.(bool)
		return ok
	case attrTypeInt:
		f, ok := v.(float64)
		return ok && f == float64(int64(f))
	case attrTypeNumber:
		_, ok := v.(float64)
		return ok
	default:
		return false
	}
}
