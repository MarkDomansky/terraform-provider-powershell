package provider

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// These tests cover the pure data-shaping helpers shared by the resource and
// data source; they need no pshost sidecar and run on every platform.

func TestMergeInputData(t *testing.T) {
	tests := []struct {
		name      string
		input     types.String
		sensitive types.String
		want      map[string]interface{}
		wantErr   bool
	}{
		{
			name:      "both null",
			input:     types.StringNull(),
			sensitive: types.StringNull(),
			want:      nil,
		},
		{
			name:      "input only",
			input:     types.StringValue(`{"name":"web01"}`),
			sensitive: types.StringNull(),
			want:      map[string]interface{}{"name": "web01"},
		},
		{
			name:      "sensitive only",
			input:     types.StringNull(),
			sensitive: types.StringValue(`{"token":"abc"}`),
			want:      map[string]interface{}{"token": "abc"},
		},
		{
			name:      "sensitive wins on collision",
			input:     types.StringValue(`{"name":"web01","token":"placeholder"}`),
			sensitive: types.StringValue(`{"token":"abc"}`),
			want:      map[string]interface{}{"name": "web01", "token": "abc"},
		},
		{
			name:    "malformed input is an error, not silently nil",
			input:   types.StringValue(`{not json`),
			wantErr: true,
		},
		{
			name:      "malformed sensitive is an error",
			input:     types.StringValue(`{"name":"web01"}`),
			sensitive: types.StringValue(`[1,2,3]`), // valid JSON but not an object
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeInputData(tt.input, tt.sensitive)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got map %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("merged map = %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("merged[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestSplitOutputData(t *testing.T) {
	t.Run("no sensitive key", func(t *testing.T) {
		output, sensitive, err := splitOutputData(map[string]interface{}{"id": "res-1", "path": "/tmp/x"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s := sensitive.ValueString(); s != "{}" {
			t.Errorf("sensitive_output_data = %q, want {}", s)
		}
		for _, want := range []string{`"id":"res-1"`, `"path":"/tmp/x"`} {
			if got := output.ValueString(); !strings.Contains(got, want) {
				t.Errorf("output_data = %q, want it to contain %s", got, want)
			}
		}
	})

	t.Run("sensitive key is extracted and removed", func(t *testing.T) {
		output, sensitive, err := splitOutputData(map[string]interface{}{
			"id":        "res-1",
			"sensitive": map[string]interface{}{"secret": "abc"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := sensitive.ValueString(); got != `{"secret":"abc"}` {
			t.Errorf("sensitive_output_data = %q, want {\"secret\":\"abc\"}", got)
		}
		if got := output.ValueString(); strings.Contains(got, "secret") || strings.Contains(got, "sensitive") {
			t.Errorf("output_data = %q leaked the sensitive key", got)
		}
	})

	t.Run("empty output", func(t *testing.T) {
		output, sensitive, err := splitOutputData(map[string]interface{}{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := output.ValueString(); got != "{}" {
			t.Errorf("output_data = %q, want {}", got)
		}
		if got := sensitive.ValueString(); got != "{}" {
			t.Errorf("sensitive_output_data = %q, want {}", got)
		}
	})
}
