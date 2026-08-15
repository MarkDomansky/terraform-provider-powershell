package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Protocol markers framing each message on the stdin/stdout pipes. Go and the
// PowerShell responder communicate in line-delimited JSON; because the script
// itself may emit arbitrary output (Write-Host, errors, banners), these unique
// sentinel lines mark where a real command or response begins and ends so the
// reader can ignore everything in between. The values must match responder.ps1.
const (
	cmdStartMarker = "###PS_CMD_START###" // Go -> PS: command JSON follows
	cmdEndMarker   = "###PS_CMD_END###"   // Go -> PS: end of command JSON
	rspStartMarker = "###PS_RSP_START###" // PS -> Go: response JSON follows
	rspEndMarker   = "###PS_RSP_END###"   // PS -> Go: end of response JSON
)

// PSCommand is the JSON request sent to the PowerShell process for one action.
// ProviderData and Session are only populated on the one-time "configure" command
// the provider sends before the startup script.
type PSCommand struct {
	Action       string                 `json:"action"`                  // configure | create | read | update | delete | startup | shutdown
	Script       string                 `json:"script"`                  // the PowerShell scriptblock to run
	InputData    map[string]interface{} `json:"input_data"`              // passed to the script as the bound $InputData parameter
	ProviderData map[string]interface{} `json:"provider_data,omitempty"` // connection args exposed to scripts as $global:ProviderData (configure only)
	Session      *PSSessionConfig       `json:"session,omitempty"`       // optional remote-session details (configure only)
}

// PSSessionConfig describes an optional remote PowerShell session for the host to
// open (modeling New-PSSession / Enter-PSSession). When present on the configure
// command, every subsequent script runs in that remote runspace. Field names are
// kept in sync with the SessionConfig class in csharp/PSHost/RunspaceManager.cs.
type PSSessionConfig struct {
	Type              string `json:"type"`                         // winrm | ssh | vmguest
	Host              string `json:"host,omitempty"`               // remote computer / hostname (winrm, ssh)
	Port              int64  `json:"port,omitempty"`               // optional port override
	Username          string `json:"username,omitempty"`           // session credential user
	Password          string `json:"password,omitempty"`           // session credential password
	UseSSL            bool   `json:"use_ssl,omitempty"`            // winrm: connect over HTTPS
	Authentication    string `json:"authentication,omitempty"`     // winrm: Default/Basic/Negotiate/Kerberos/Credssp/...
	CertThumbprint    string `json:"cert_thumbprint,omitempty"`    // winrm: client-certificate auth thumbprint
	ConfigurationName string `json:"configuration_name,omitempty"` // winrm: session configuration endpoint
	KeyFile           string `json:"key_file,omitempty"`           // ssh: private key file path
	VMName            string `json:"vm_name,omitempty"`            // vmguest: VM name (PowerShell Direct)
	VMId              string `json:"vm_id,omitempty"`              // vmguest: VM GUID (PowerShell Direct)
}

// PSResponse is the JSON reply received from the PowerShell process after an action.
type PSResponse struct {
	Success    bool                   `json:"success"`     // false if the script threw or wrote to the error stream
	OutputData map[string]interface{} `json:"output_data"` // the single object the script emitted to the output stream
	Error      string                 `json:"error"`       // error message when Success is false
}

// PSManager manages the lifecycle of a persistent PowerShell process and
// provides thread-safe command execution via stdin/stdout JSON protocol.
type PSManager struct {
	cmd    *exec.Cmd      // the running sidecar process
	stdin  io.WriteCloser // pipe we write commands to
	stdout *bufio.Scanner // pipe we read responses from, line by line
	stderr io.ReadCloser  // sidecar's stderr (drained on Close)
	mu     sync.Mutex     // serializes Execute so only one command is in flight at a time

	defaultTimeout time.Duration // used when a command passes timeout == 0

	// shutdownScript is run once during Close, before the process is torn down.
	shutdownScript  string
	shutdownTimeout time.Duration
	closed          bool
	// terminated is set when a command times out and the sidecar is force-killed.
	// Unlike closed (graceful), it makes every subsequent Execute fail fast, because
	// the process is gone and its runspace state is unrecoverable.
	terminated bool
}

// defaultTimeoutSeconds is the fallback script-execution timeout used when neither
// the provider nor the resource specifies one. Referenced by both the provider's
// Configure and NewPSManager so the default lives in exactly one place.
const defaultTimeoutSeconds = 3600

// NewPSManager starts the pshost sidecar process running the PowerShell responder loop.
func NewPSManager(ctx context.Context, defaultTimeout time.Duration) (*PSManager, error) {
	if defaultTimeout == 0 {
		defaultTimeout = defaultTimeoutSeconds * time.Second
	}

	sidecarPath, err := findSidecarBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to locate pshost sidecar binary: %w", err)
	}

	// The sidecar must outlive the call that starts it. Under real Terraform the
	// provider's Configure context is cancelled as soon as Configure returns, so
	// binding the process to it via exec.CommandContext(ctx, ...) would kill the
	// sidecar before any resource operation runs ("pipe is being closed"). The
	// sidecar's lifetime is instead bounded explicitly by Close, so it is started
	// detached from the request context. (ctx is retained in the signature for
	// callers and potential future use such as startup logging.)
	_ = ctx
	cmd := exec.Command(sidecarPath)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start pshost sidecar process: %w", err)
	}

	// Each JSON response arrives as a single line. bufio.Scanner caps line length
	// at 64KB by default, which a large output object would exceed and truncate, so
	// raise the limit to 10MB (start at 64KB, grow as needed).
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	mgr := &PSManager{
		cmd:            cmd,
		stdin:          stdin,
		stdout:         scanner,
		stderr:         stderr,
		defaultTimeout: defaultTimeout,
	}

	// There is no readiness handshake: the sidecar opens its runspace synchronously
	// before reading stdin, so the first command the provider sends (always the
	// "configure" command from Configure) blocks on its response until the runspace
	// is up. That response is the de-facto ready signal.
	return mgr, nil
}

// Configure sends the one-time "configure" command, which seeds $global:ProviderData
// and, when session details are supplied, opens the remote PowerShell session that
// every subsequent script runs in. The provider calls this once, before the startup
// script, so connection details and provider arguments are in place first.
func (m *PSManager) Configure(providerData map[string]interface{}, session *PSSessionConfig, timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	resp, err := m.roundTripLocked(PSCommand{
		Action:       "configure",
		ProviderData: providerData,
		Session:      session,
	}, timeout)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// Execute sends a CRUD/lifecycle command to the PowerShell process and returns the
// response. It is safe to call from multiple goroutines; a mutex serializes access.
func (m *PSManager) Execute(action, script string, inputData map[string]interface{}, timeout time.Duration) (*PSResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.roundTripLocked(PSCommand{
		Action:    action,
		Script:    script,
		InputData: inputData,
	}, timeout)
}

// roundTripLocked marshals one command, writes it framed on stdin, and reads the
// framed response, racing the read against the timeout. The caller must hold m.mu.
func (m *PSManager) roundTripLocked(cmd PSCommand, timeout time.Duration) (*PSResponse, error) {
	// A prior timeout force-killed the sidecar; it cannot run anything else.
	if m.terminated {
		return nil, fmt.Errorf("PowerShell sidecar was terminated after a previous command timed out and can no longer execute scripts")
	}

	if timeout == 0 {
		timeout = m.defaultTimeout
	}

	cmdJSON, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal command: %w", err)
	}

	// Write the command framed by start/end markers so the responder knows exactly
	// which lines form the JSON payload.
	if _, err := fmt.Fprintln(m.stdin, cmdStartMarker); err != nil {
		return nil, fmt.Errorf("failed to write command start marker: %w", err)
	}
	if _, err := fmt.Fprintln(m.stdin, string(cmdJSON)); err != nil {
		return nil, fmt.Errorf("failed to write command JSON: %w", err)
	}
	if _, err := fmt.Fprintln(m.stdin, cmdEndMarker); err != nil {
		return nil, fmt.Errorf("failed to write command end marker: %w", err)
	}

	// Read the response on a goroutine so the blocking stdout scan can race against
	// a timeout. The channels are buffered (size 1) so the goroutine never blocks
	// sending even if we have already returned on the timeout branch.
	responseCh := make(chan *PSResponse, 1)
	errCh := make(chan error, 1)

	go func() {
		resp, err := m.readResponse()
		if err != nil {
			errCh <- err
			return
		}
		responseCh <- resp
	}()

	select {
	case resp := <-responseCh:
		return resp, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(timeout):
		// The sidecar is still executing the timed-out script and the reader
		// goroutine above is still blocked on m.stdout. This process cannot be
		// reused: if we returned and left it alive, the next Execute would start a
		// second goroutine scanning the same bufio.Scanner (which is not safe for
		// concurrent use) and could interleave with the stale script's eventual
		// response, corrupting the protocol. Force-kill the sidecar instead. That
		// unblocks the orphaned reader (its Scan returns false on the closed pipe,
		// so it exits via the buffered errCh) and marks the manager terminated so
		// later Execute calls fail fast with a clear error.
		m.terminateLocked()
		return nil, fmt.Errorf("command timed out after %v; PowerShell sidecar was terminated", timeout)
	}
}

// terminateLocked force-kills the sidecar process and marks the manager unusable.
// It must be called with m.mu held. It is the timeout path's counterpart to the
// graceful Close: there is no clean handshake because the process is in an unknown
// state mid-script, so the process is simply killed and the pipes closed.
func (m *PSManager) terminateLocked() {
	if m.closed {
		return
	}
	m.closed = true
	m.terminated = true
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
	}
	if m.stdin != nil {
		_ = m.stdin.Close()
	}
	if m.stderr != nil {
		_ = m.stderr.Close()
	}
}

// readResponse reads stdout lines until a complete response is found between markers.
func (m *PSManager) readResponse() (*PSResponse, error) {
	var jsonLines []string
	inResponse := false

	// Scan stdout line by line. The script's own stdout chatter is interleaved with
	// the protocol, so we only collect lines that fall between the response markers.
	for m.stdout.Scan() {
		line := m.stdout.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == rspStartMarker {
			// Begin (or restart) accumulating; discard anything seen so far.
			inResponse = true
			jsonLines = nil
			continue
		}

		if trimmed == rspEndMarker {
			if !inResponse {
				// An end marker with no preceding start marker; ignore it.
				continue
			}
			// Parse accumulated JSON
			jsonPayload := strings.Join(jsonLines, "\n")
			var resp PSResponse
			if err := json.Unmarshal([]byte(jsonPayload), &resp); err != nil {
				return nil, fmt.Errorf("failed to parse response JSON: %w\nRaw: %s", err, jsonPayload)
			}
			return &resp, nil
		}

		if inResponse {
			jsonLines = append(jsonLines, line)
		}
		// Lines outside response markers are ignored (script Write-Host output, etc.)
	}

	if err := m.stdout.Err(); err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}
	return nil, fmt.Errorf("pshost sidecar exited without sending a response")
}

// SetShutdownScript registers a script that Close runs exactly once, in the same
// persistent PowerShell process, just before the process is terminated. Because it
// shares the process (and remote session, if any) with every resource operation, it
// can observe any globals accumulated during the run.
func (m *PSManager) SetShutdownScript(script string, timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shutdownScript = script
	m.shutdownTimeout = timeout
}

// Close runs the registered shutdown script (if any) and then shuts down the
// PowerShell process gracefully. It is idempotent and safe to call multiple
// times: the shutdown script and teardown run only on the first call.
func (m *PSManager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	shutdownScript := m.shutdownScript
	shutdownTimeout := m.shutdownTimeout
	m.mu.Unlock()

	// Run the shutdown script first, while the process is still alive. Execute
	// acquires the mutex itself; the closed flag guarantees this happens once.
	var shutdownErr error
	if shutdownScript != "" {
		result, err := m.Execute("shutdown", shutdownScript, nil, shutdownTimeout)
		if err != nil {
			shutdownErr = fmt.Errorf("shutdown script execution failed: %w", err)
		} else if !result.Success {
			shutdownErr = fmt.Errorf("shutdown script returned an error: %s", result.Error)
		}
	}

	if m.stdin != nil {
		// Send exit to break the loop, then close stdin
		_, _ = fmt.Fprintln(m.stdin, "exit")
		_ = m.stdin.Close()
	}
	if m.stderr != nil {
		_ = m.stderr.Close()
	}
	if m.cmd != nil && m.cmd.Process != nil {
		// Wait with a timeout, then force kill
		done := make(chan error, 1)
		go func() {
			done <- m.cmd.Wait()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = m.cmd.Process.Kill()
		}
	}
	return shutdownErr
}

// findSidecarBinary locates the pshost sidecar executable.
// It checks the PSHOST_PATH env var first, then next to the running executable, then PATH.
func findSidecarBinary() (string, error) {
	// Check PSHOST_PATH env var first (useful for development and testing)
	if envPath := os.Getenv("PSHOST_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
		return "", fmt.Errorf("PSHOST_PATH is set to %q but the file does not exist", envPath)
	}

	binaryName := "pshost"
	if runtime.GOOS == "windows" {
		binaryName = "pshost.exe"
	}

	// Check next to the running executable
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), binaryName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// Fall back to PATH lookup
	if path, err := exec.LookPath(binaryName); err == nil {
		return path, nil
	}

	return "", fmt.Errorf(
		"could not find %q next to the provider binary or on PATH. "+
			"Ensure the pshost sidecar binary is present alongside the provider executable, "+
			"or set PSHOST_PATH to its location",
		binaryName,
	)
}
