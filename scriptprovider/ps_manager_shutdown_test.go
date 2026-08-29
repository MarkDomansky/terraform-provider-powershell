package scriptprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// countLines returns the number of non-empty lines in a file, or 0 if it does
// not exist. Tests use a log file appended to by a script as a tamper-proof
// counter of how many times that script actually executed.
func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("failed to read %s: %v", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

// readTrimmed returns the file contents with surrounding whitespace removed.
func readTrimmed(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return strings.TrimSpace(string(data))
}

// TestCloseRunsShutdownScriptOnce verifies a registered shutdown script runs on
// Close, and that Close is idempotent: calling it again is a no-op rather than
// running the shutdown script a second time.
func TestCloseRunsShutdownScriptOnce(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	shutdownLog := filepath.ToSlash(filepath.Join(dir, "shutdown.log"))
	mgr.SetShutdownScript(`Add-Content -Path '`+shutdownLog+`' -Value "closed pid=$PID"`, 0)

	if err := mgr.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	// A second Close must not run the shutdown script again.
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}

	if got := countLines(t, shutdownLog); got != 1 {
		t.Fatalf("expected shutdown script to run exactly once, ran %d times", got)
	}
}

// TestCloseWithoutShutdownScript verifies Close still works when no shutdown
// script has been registered.
func TestCloseWithoutShutdownScript(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

// TestShutdownScriptErrorSurfaced verifies a failing shutdown script is reported
// by Close rather than silently swallowed.
func TestShutdownScriptErrorSurfaced(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	mgr.SetShutdownScript(`throw "shutdown boom"`, 0)
	if err := mgr.Close(); err == nil {
		t.Fatal("expected Close to return the shutdown script error, got nil")
	}
}

// TestShutdownScriptSharesProcessAndStateWithResources proves that the shutdown
// script runs in the very same PowerShell process as the resource operations and
// can observe globals those resources accumulated. State sharing now uses ordinary
// globals a script creates (here $global:HostPid / $global:ResourceCount), not a
// provider-managed bag.
func TestShutdownScriptSharesProcessAndStateWithResources(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewPSManager(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create PSManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	dir := t.TempDir()
	shutdownLog := filepath.ToSlash(filepath.Join(dir, "shutdown.log"))

	// Seed shared globals, recording the host process PID.
	if _, err := mgr.Execute("startup",
		`$global:HostPid = "$PID"; $global:ResourceCount = 0`, nil, 0); err != nil {
		t.Fatalf("startup execute failed: %v", err)
	}

	// Run several resources through the same manager; each must run in the same
	// process (pid == host_pid) and increment the shared counter.
	const resourceCount = 3
	var hostPID string
	for i := 0; i < resourceCount; i++ {
		resp, err := mgr.Execute("create", `
			$global:ResourceCount = [int]$global:ResourceCount + 1
			[PSCustomObject]@{ pid = "$PID"; host_pid = "$($global:HostPid)" }
		`, nil, 0)
		if err != nil {
			t.Fatalf("resource %d execute failed: %v", i, err)
		}
		if !resp.Success {
			t.Fatalf("resource %d script error: %s", i, resp.Error)
		}
		pid := fmt.Sprintf("%v", resp.OutputData["pid"])
		host := fmt.Sprintf("%v", resp.OutputData["host_pid"])
		if pid != host {
			t.Errorf("resource %d ran in a different process: pid=%s host_pid=%s", i, pid, host)
		}
		hostPID = pid
	}

	// Register a shutdown script that records the PID and the accumulated count.
	mgr.SetShutdownScript(
		`Add-Content -Path '`+shutdownLog+`' -Value "pid=$PID resource_count=$($global:ResourceCount)"`, 0)
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	line := readTrimmed(t, shutdownLog)
	if got := countLines(t, shutdownLog); got != 1 {
		t.Fatalf("expected shutdown to run once, got %d lines: %q", got, line)
	}
	if !strings.Contains(line, "pid="+hostPID+" ") {
		t.Errorf("shutdown ran in a different process than the resources: line=%q, want pid=%s", line, hostPID)
	}
	if !strings.Contains(line, fmt.Sprintf("resource_count=%d", resourceCount)) {
		t.Errorf("shutdown did not observe all resource operations: line=%q, want resource_count=%d", line, resourceCount)
	}
}
