package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestNewPSManager(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()
}

func TestExecuteSimpleScript(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	resp, err := mgr.Execute("create", `[PSCustomObject]@{ id = "test-123"; status = "created" }`, nil, 0)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Script failed: %s", resp.Error)
	}
	if resp.OutputData["id"] != "test-123" {
		t.Errorf("Expected id 'test-123', got '%v'", resp.OutputData["id"])
	}
	if resp.OutputData["status"] != "created" {
		t.Errorf("Expected status 'created', got '%v'", resp.OutputData["status"])
	}
}

func TestExecuteWithInputData(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	inputData := map[string]interface{}{
		"name":     "my-resource",
		"location": "eastus",
	}

	script := `[PSCustomObject]@{ id = $InputData.name; location = $InputData.location; combined = "$($InputData.name)-$($InputData.location)" }`
	resp, err := mgr.Execute("create", script, inputData, 0)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Script failed: %s", resp.Error)
	}
	if resp.OutputData["id"] != "my-resource" {
		t.Errorf("Expected id 'my-resource', got '%v'", resp.OutputData["id"])
	}
	if resp.OutputData["combined"] != "my-resource-eastus" {
		t.Errorf("Expected combined 'my-resource-eastus', got '%v'", resp.OutputData["combined"])
	}
}

func TestExecuteScriptError(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	resp, err := mgr.Execute("create", `throw "something went wrong"`, nil, 0)
	if err != nil {
		t.Fatalf("Execute returned transport error (expected script error in response): %v", err)
	}
	if resp.Success {
		t.Fatal("Expected script to fail, but it succeeded")
	}
	if resp.Error == "" {
		t.Fatal("Expected non-empty error message")
	}
}

// TestGlobalVariablePersistsAcrossCalls proves the runspace is persistent: a global
// a script creates in one call is visible to a later call. (The provider no longer
// manages a $global:ProviderState bag; scripts roll their own globals when needed.)
func TestGlobalVariablePersistsAcrossCalls(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	// First call: create a global of our own.
	resp, err := mgr.Execute("startup", `$global:MyToken = "abc123"`, nil, 0)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Script failed: %s", resp.Error)
	}

	// Second call: read it back.
	resp, err = mgr.Execute("read", `[PSCustomObject]@{ token = $global:MyToken }`, nil, 0)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Script failed: %s", resp.Error)
	}
	if resp.OutputData["token"] != "abc123" {
		t.Errorf("Expected token 'abc123', got '%v'", resp.OutputData["token"])
	}
}

func TestMultipleSequentialExecutions(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	for i := 0; i < 5; i++ {
		script := `[PSCustomObject]@{ id = "item-$($InputData.index)" }`
		inputData := map[string]interface{}{"index": i}
		resp, err := mgr.Execute("create", script, inputData, 0)
		if err != nil {
			t.Fatalf("Execute %d failed: %v", i, err)
		}
		if !resp.Success {
			t.Fatalf("Script %d failed: %s", i, resp.Error)
		}
	}
}

func TestComplexJsonRoundTrip(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	inputData := map[string]interface{}{
		"name": "test",
		"tags": map[string]interface{}{
			"env":  "dev",
			"team": "platform",
		},
		"count": 42,
	}

	script := `
[PSCustomObject]@{
	id = "complex-1"
	name = $InputData.name
	tag_count = $InputData.tags.Count
	doubled_count = [int]$InputData.count * 2
}
`
	resp, err := mgr.Execute("create", script, inputData, 0)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Script failed: %s", resp.Error)
	}
	if resp.OutputData["name"] != "test" {
		t.Errorf("Expected name 'test', got '%v'", resp.OutputData["name"])
	}

	// Verify output_data can be marshaled back to JSON
	_, err = json.Marshal(resp.OutputData)
	if err != nil {
		t.Fatalf("Failed to marshal output: %v", err)
	}
}

func TestExecuteEmptyOutput(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	// Script that emits nothing to the output stream (simulates resource not found)
	resp, err := mgr.Execute("read", `# resource not found, emit nothing`, nil, 0)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Script failed: %s", resp.Error)
	}
	if len(resp.OutputData) != 0 {
		t.Errorf("Expected empty output_data, got %v", resp.OutputData)
	}
}

func TestTimeout(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	_, err = mgr.Execute("create", `Start-Sleep -Seconds 30`, nil, 2*time.Second)
	if err == nil {
		t.Fatal("Expected timeout error, got nil")
	}

	// After a timeout the sidecar is force-killed and the manager is fenced: the
	// orphaned reader is gone, so a subsequent Execute must fail fast rather than
	// start a second goroutine scanning the same stdout pipe (the corruption the
	// timeout teardown prevents). A short script that would normally succeed in
	// well under the timeout must still error out.
	_, err = mgr.Execute("create", `[PSCustomObject]@{ id = "after-timeout" }`, nil, 5*time.Second)
	if err == nil {
		t.Fatal("Expected Execute after a timeout to fail because the sidecar was terminated, got nil")
	}
}

func TestClose(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}

	// Should not error
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestMultipleResourcesCrudCycle(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	// Simulate two independent resources going through full CRUD lifecycle
	// through the same PSManager (same sidecar process)

	// Resource A: create
	respA, err := mgr.Execute("create", `[PSCustomObject]@{ id = "res-a"; value = $InputData.val }`,
		map[string]interface{}{"val": "alpha"}, 0)
	if err != nil {
		t.Fatalf("Create A failed: %v", err)
	}
	if !respA.Success {
		t.Fatalf("Create A script error: %s", respA.Error)
	}
	if respA.OutputData["id"] != "res-a" {
		t.Errorf("Expected id 'res-a', got '%v'", respA.OutputData["id"])
	}

	// Resource B: create
	respB, err := mgr.Execute("create", `[PSCustomObject]@{ id = "res-b"; value = $InputData.val }`,
		map[string]interface{}{"val": "beta"}, 0)
	if err != nil {
		t.Fatalf("Create B failed: %v", err)
	}
	if !respB.Success {
		t.Fatalf("Create B script error: %s", respB.Error)
	}
	if respB.OutputData["id"] != "res-b" {
		t.Errorf("Expected id 'res-b', got '%v'", respB.OutputData["id"])
	}

	// Resource A: read
	respA, err = mgr.Execute("read", `[PSCustomObject]@{ id = $InputData.id; value = "alpha" }`,
		map[string]interface{}{"id": "res-a"}, 0)
	if err != nil {
		t.Fatalf("Read A failed: %v", err)
	}
	if !respA.Success {
		t.Fatalf("Read A script error: %s", respA.Error)
	}

	// Resource B: update
	respB, err = mgr.Execute("update", `[PSCustomObject]@{ id = $InputData.id; value = "beta-updated" }`,
		map[string]interface{}{"id": "res-b"}, 0)
	if err != nil {
		t.Fatalf("Update B failed: %v", err)
	}
	if !respB.Success {
		t.Fatalf("Update B script error: %s", respB.Error)
	}
	if respB.OutputData["value"] != "beta-updated" {
		t.Errorf("Expected value 'beta-updated', got '%v'", respB.OutputData["value"])
	}

	// Resource A: delete
	respA, err = mgr.Execute("delete", `# deleted`, map[string]interface{}{"id": "res-a"}, 0)
	if err != nil {
		t.Fatalf("Delete A failed: %v", err)
	}
	if !respA.Success {
		t.Fatalf("Delete A script error: %s", respA.Error)
	}

	// Resource B: delete
	respB, err = mgr.Execute("delete", `# deleted`, map[string]interface{}{"id": "res-b"}, 0)
	if err != nil {
		t.Fatalf("Delete B failed: %v", err)
	}
	if !respB.Success {
		t.Fatalf("Delete B script error: %s", respB.Error)
	}
}

// TestIncidentalVariableDoesNotLeak proves that a bare (unscoped) variable created
// by one resource's CRUD script is not visible to the next resource's script: CRUD
// scripts run in an isolated child scope.
func TestIncidentalVariableDoesNotLeak(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	// Resource A leaves a bare variable behind.
	respA, err := mgr.Execute("create", `$leaked = "from-a"; [PSCustomObject]@{ id = "a" }`, nil, 0)
	if err != nil {
		t.Fatalf("Create A failed: %v", err)
	}
	if !respA.Success {
		t.Fatalf("Create A script error: %s", respA.Error)
	}

	// Resource B must observe $leaked as empty (isolated, not leaked).
	respB, err := mgr.Execute("create", `[PSCustomObject]@{ id = "b"; seen = "$leaked" }`, nil, 0)
	if err != nil {
		t.Fatalf("Create B failed: %v", err)
	}
	if !respB.Success {
		t.Fatalf("Create B script error: %s", respB.Error)
	}
	if respB.OutputData["seen"] != "" {
		t.Errorf("Expected leaked variable to be isolated (empty), got '%v'", respB.OutputData["seen"])
	}
}

// TestConfigureExposesProviderData proves the one-time configure command seeds
// $global:ProviderData with the connection arguments (including the nested
// Data object from the providerdata argument) and that later scripts can read them.
func TestConfigureExposesProviderData(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	providerData := map[string]interface{}{
		"server":   "db01.example.com",
		"username": "svc-deploy",
		"Data": map[string]interface{}{
			"environment": "prod",
		},
	}
	if err := mgr.Configure(providerData, nil, 0); err != nil {
		t.Fatalf("Configure failed: %v", err)
	}

	resp, err := mgr.Execute("read", `
		[PSCustomObject]@{
			id          = "x"
			server      = $global:ProviderData.server
			username    = $global:ProviderData.username
			environment = $global:ProviderData.Data.environment
		}
	`, nil, 0)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Script failed: %s", resp.Error)
	}
	if resp.OutputData["server"] != "db01.example.com" {
		t.Errorf("Expected server 'db01.example.com', got '%v'", resp.OutputData["server"])
	}
	if resp.OutputData["username"] != "svc-deploy" {
		t.Errorf("Expected username 'svc-deploy', got '%v'", resp.OutputData["username"])
	}
	if resp.OutputData["environment"] != "prod" {
		t.Errorf("Expected nested Data.environment 'prod', got '%v'", resp.OutputData["environment"])
	}
}
