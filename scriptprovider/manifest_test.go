package scriptprovider

import (
	"strings"
	"testing"
)

func TestParseManifestValid(t *testing.T) {
	data := []byte(`{
		"version": 1,
		"description": "A thing.",
		"timeout_seconds": 600,
		"attributes": {
			"name":    {"type": "string", "required": true, "requires_replace": true},
			"hidden":  {"type": "bool", "optional": true, "default": false},
			"members": {"type": "set", "element_type": "string", "optional": true},
			"tags":    {"type": "map", "element_type": "string", "optional": true},
			"quota":   {"type": "int", "optional": true, "validators": [{"one_of": [10, 50, 100]}]},
			"ratio":   {"type": "number", "optional": true},
			"extra":   {"type": "json", "optional": true},
			"status":  {"type": "string", "computed": true},
			"token":   {"type": "string", "computed": true, "sensitive": true}
		}
	}`)
	m, err := parseManifest(data, manifestResource, "test/schema.json")
	if err != nil {
		t.Fatalf("expected valid manifest, got: %v", err)
	}
	if m.TimeoutSeconds != 600 {
		t.Errorf("timeout_seconds = %d, want 600", m.TimeoutSeconds)
	}
	if got := len(m.configNames()); got != 7 {
		t.Errorf("config attrs = %d, want 7", got)
	}
	if got := len(m.computedNames()); got != 2 {
		t.Errorf("computed attrs = %d, want 2", got)
	}
	if m.Attributes["hidden"].defaultValue != false {
		t.Errorf("hidden default = %v, want false", m.Attributes["hidden"].defaultValue)
	}
}

func TestParseManifestErrors(t *testing.T) {
	cases := []struct {
		name    string
		kind    manifestKind
		json    string
		wantErr string
	}{
		{"unknown top-level key", manifestResource,
			`{"version": 1, "bogus": true, "attributes": {"a": {"type": "string", "required": true}}}`,
			"bogus"},
		{"unknown attribute key", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "string", "required": true, "nope": 1}}}`,
			"nope"},
		{"wrong version", manifestResource,
			`{"version": 2, "attributes": {"a": {"type": "string", "required": true}}}`,
			`"version" must be 1`},
		{"no attributes", manifestResource,
			`{"version": 1, "attributes": {}}`,
			"at least one attribute"},
		{"reserved id", manifestResource,
			`{"version": 1, "attributes": {"id": {"type": "string", "computed": true}}}`,
			`"id" is reserved`},
		{"bad attribute name", manifestResource,
			`{"version": 1, "attributes": {"BadName": {"type": "string", "required": true}}}`,
			"must match"},
		{"unknown type", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "object", "required": true}}}`,
			"unknown type"},
		{"collection without element_type", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "list", "required": true}}}`,
			`"element_type" is required`},
		{"element_type on primitive", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "string", "element_type": "string", "required": true}}}`,
			"only applies to list"},
		{"two behaviors", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "string", "required": true, "computed": true}}}`,
			"exactly one of"},
		{"no behavior", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "string"}}}`,
			"exactly one of"},
		{"computed provider attr", manifestProvider,
			`{"version": 1, "attributes": {"a": {"type": "string", "computed": true}}}`,
			"cannot be computed"},
		{"requires_replace on data source", manifestDataSource,
			`{"version": 1, "attributes": {"a": {"type": "string", "required": true, "requires_replace": true}}}`,
			"only applies to resource"},
		{"requires_replace on computed", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "string", "computed": true, "requires_replace": true}}}`,
			"cannot be set on a computed"},
		{"env outside provider", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "string", "optional": true, "env": "X"}}}`,
			`"env" only applies to provider`},
		{"env on int", manifestProvider,
			`{"version": 1, "attributes": {"a": {"type": "int", "optional": true, "env": "X"}}}`,
			"only applies to string"},
		{"default on required", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "string", "required": true, "default": "x"}}}`,
			`"default" only applies to optional`},
		{"default type mismatch", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "int", "optional": true, "default": "x"}}}`,
			"does not match the declared type"},
		{"default non-integral int", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "int", "optional": true, "default": 1.5}}}`,
			"does not match the declared type"},
		{"one_of wrong element kind", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "string", "optional": true, "validators": [{"one_of": [1]}]}}}`,
			"must be strings"},
		{"one_of on bool", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "bool", "optional": true, "validators": [{"one_of": [true]}]}}}`,
			"only applies to string, int, and number"},
		{"unknown validator key", manifestResource,
			`{"version": 1, "attributes": {"a": {"type": "string", "optional": true, "validators": [{"pattern": "x"}]}}}`,
			"pattern"},
		{"timeout in provider manifest", manifestProvider,
			`{"version": 1, "timeout_seconds": 5, "attributes": {"a": {"type": "string", "optional": true}}}`,
			"not allowed in a provider manifest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseManifest([]byte(tc.json), tc.kind, "test/schema.json")
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestParseManifestDefaults(t *testing.T) {
	data := []byte(`{
		"version": 1,
		"attributes": {
			"count": {"type": "int", "optional": true, "default": 4},
			"names": {"type": "list", "element_type": "string", "optional": true, "default": ["a", "b"]},
			"opts":  {"type": "json", "optional": true, "default": {"x": 1}}
		}
	}`)
	m, err := parseManifest(data, manifestDataSource, "test/schema.json")
	if err != nil {
		t.Fatalf("expected valid manifest, got: %v", err)
	}
	if v, ok := m.Attributes["count"].defaultValue.(float64); !ok || v != 4 {
		t.Errorf("count default = %v (%T), want 4", m.Attributes["count"].defaultValue, m.Attributes["count"].defaultValue)
	}
	if v, ok := m.Attributes["names"].defaultValue.([]interface{}); !ok || len(v) != 2 {
		t.Errorf("names default = %v, want [a b]", m.Attributes["names"].defaultValue)
	}
	if _, ok := m.Attributes["opts"].defaultValue.(map[string]interface{}); !ok {
		t.Errorf("opts default = %T, want map", m.Attributes["opts"].defaultValue)
	}
}

func TestBuildSchemasFromManifest(t *testing.T) {
	data := []byte(`{
		"version": 1,
		"description": "Sample.",
		"attributes": {
			"name":   {"type": "string", "required": true, "requires_replace": true},
			"quota":  {"type": "int", "optional": true, "validators": [{"one_of": [1, 2]}]},
			"tags":   {"type": "map", "element_type": "string", "optional": true},
			"status": {"type": "string", "computed": true}
		}
	}`)
	m, err := parseManifest(data, manifestResource, "test/schema.json")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	rs, err := buildResourceSchema(m)
	if err != nil {
		t.Fatalf("buildResourceSchema: %v", err)
	}
	if _, ok := rs.Attributes["id"]; !ok {
		t.Error("resource schema missing automatic id attribute")
	}
	if len(rs.Attributes) != 5 {
		t.Errorf("resource schema has %d attributes, want 5", len(rs.Attributes))
	}

	// The same attribute set (minus resource-only settings) is valid for a
	// data source.
	dsData := []byte(`{
		"version": 1,
		"description": "Sample.",
		"attributes": {
			"name":   {"type": "string", "required": true},
			"quota":  {"type": "int", "optional": true, "validators": [{"one_of": [1, 2]}]},
			"tags":   {"type": "map", "element_type": "string", "optional": true},
			"status": {"type": "string", "computed": true}
		}
	}`)
	dm, err := parseManifest(dsData, manifestDataSource, "test/schema.json")
	if err != nil {
		t.Fatalf("parse as data source: %v", err)
	}
	ds, err := buildDataSourceSchema(dm)
	if err != nil {
		t.Fatalf("buildDataSourceSchema: %v", err)
	}
	if len(ds.Attributes) != 4 {
		t.Errorf("data source schema has %d attributes, want 4", len(ds.Attributes))
	}

	// Provider manifests reject computed attributes, so drop "status".
	pdata := []byte(`{
		"version": 1,
		"attributes": {
			"endpoint": {"type": "string", "optional": true, "env": "SAMPLE_ENDPOINT"}
		}
	}`)
	pm, err := parseManifest(pdata, manifestProvider, "test/schema.json")
	if err != nil {
		t.Fatalf("parse provider manifest: %v", err)
	}
	pa, err := buildProviderAttributes(pm)
	if err != nil {
		t.Fatalf("buildProviderAttributes: %v", err)
	}
	if len(pa) != 1 {
		t.Errorf("provider attributes = %d, want 1", len(pa))
	}
}
