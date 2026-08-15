using System.Collections;
using System.Management.Automation;
using System.Management.Automation.Language;
using System.Management.Automation.Runspaces;
using System.Security;
using System.Text.Json.Serialization;

namespace PSHost;

/// <summary>
/// Manages a persistent PowerShell runspace for executing scripts. The runspace
/// stays open for the lifetime of the process, so any globals a script creates
/// (e.g. <c>$global:MyState</c>) persist across all later CRUD operations.
///
/// Scripts follow the native PowerShell streaming model: inputs arrive as the
/// bound <c>-InputData</c> parameter and the result is whatever single object the
/// script emits to the success (output) stream - typically
/// <c>[PSCustomObject]@{ id = ...; ... }</c>. There is no $OutputData variable to
/// set and no stdout markers to format. Every other stream
/// (information/warning/verbose/debug) is captured for diagnostics, and a single
/// record on the error stream marks the call as failed.
///
/// The host always receives a one-time <c>configure</c> command before the startup
/// script. It seeds <c>$global:ProviderData</c> with the provider's connection
/// arguments and, when session details are supplied, opens a remote PowerShell
/// session (WinRM/SSH/VM guest) that becomes the execution runspace for every
/// subsequent script - modeling <c>Enter-PSSession</c>: connect first, then set up
/// the provider arguments on the (remote) host.
/// </summary>
public sealed class RunspaceManager : IDisposable
{
    // The local runspace, always opened. When no remote session is configured it is
    // also the execution runspace; when one is, it is the bootstrap runspace used to
    // open and later remove the remote session.
    private readonly Runspace _bootstrapRunspace;

    // The runspace scripts actually run in: the local one, or a remote session's
    // runspace once Configure opens one.
    private Runspace _activeRunspace;

    // The remote session opened by Configure, if any. Kept so it can be closed
    // (Remove-PSSession) on Dispose, after the shutdown script has run.
    private PSSession? _session;

    private bool _disposed;

    public RunspaceManager()
    {
        // Create and open a default runspace. This loads the standard cmdlets and is
        // the runspace reused by every Execute call (unless a remote session takes
        // over). ErrorActionPreference is seeded to 'Stop' so cmdlet errors surface
        // as failures even before the configure command runs.
        var initialState = InitialSessionState.CreateDefault();
        initialState.Variables.Add(new SessionStateVariableEntry(
            "ErrorActionPreference", "Stop", "Surface cmdlet errors as failures"));
        _bootstrapRunspace = RunspaceFactory.CreateRunspace(initialState);
        _bootstrapRunspace.Open();
        _activeRunspace = _bootstrapRunspace;
    }

    /// <summary>
    /// Runs a single command request against the active runspace and returns the
    /// resulting output data and any error. Never throws: failures are reported via
    /// <see cref="CommandResponse.Success"/>. The <c>configure</c> action is handled
    /// specially by <see cref="Configure"/>.
    /// </summary>
    public CommandResponse Execute(CommandRequest request)
    {
        if (string.Equals(request.Action, "configure", StringComparison.OrdinalIgnoreCase))
        {
            return Configure(request);
        }

        // Per-call input is handed to the script as the bound -InputData parameter
        // (the PSEngine model). The action is injected into the built script so the
        // script can branch on $Action.
        var inputData = ConvertToHashtable(request.InputData);

        try
        {
            using var ps = PowerShell.Create();
            ps.Runspace = _activeRunspace;

            ps.AddScript(BuildScript(request.Action, request.Script ?? ""));
            ps.AddParameter("InputData", inputData);

            var rawOutput = ps.Invoke();

            // Capture every non-output stream for diagnostics regardless of outcome.
            LogStreams(ps, request.Action);

            // Per the PSEngine model, any record on the error stream is a failure even
            // if the script did not throw.
            if (ps.Streams.Error.Count > 0)
            {
                var errorMessages = ps.Streams.Error
                    .Select(e => e.Exception?.Message ?? e.ToString())
                    .ToArray();
                return Failure(string.Join("; ", errorMessages));
            }

            // The script communicates its result by emitting it to the success stream.
            // Drop nulls; the contract is exactly one object (with an 'id' field).
            // Zero objects is allowed - e.g. a read that finds nothing returns empty
            // output so Terraform drops the resource from state. More than one object
            // means the script leaked stray output and is treated as a failure.
            var outputs = rawOutput.Where(o => o != null && o.BaseObject != null).ToList();
            if (outputs.Count > 1)
            {
                return Failure(
                    $"Script emitted {outputs.Count} objects to the output stream; it must output exactly " +
                    "one object (with an 'id' field). Pipe any stray cmdlet output to Out-Null.");
            }

            var outputData = outputs.Count == 1
                ? ConvertResultObject(outputs[0])
                : new Dictionary<string, object?>();

            return new CommandResponse
            {
                Success = true,
                OutputData = outputData,
                Error = ""
            };
        }
        catch (Exception ex)
        {
            // Terminating errors (throw, -ErrorAction Stop, etc.) land here. For
            // PowerShell RuntimeExceptions, append the script stack trace so the
            // Terraform user can see which line of their script failed.
            var errorMessage = ex.Message;
            if (ex is RuntimeException runtimeEx && runtimeEx.ErrorRecord?.ScriptStackTrace != null)
            {
                errorMessage += "\nStack trace: " + runtimeEx.ErrorRecord.ScriptStackTrace;
            }

            return Failure(errorMessage);
        }
    }

    /// <summary>
    /// Handles the one-time <c>configure</c> command: optionally opens a remote
    /// session (which then becomes the execution runspace) and seeds
    /// <c>$global:ProviderData</c> plus ErrorActionPreference on the active runspace.
    /// Never throws; connection or setup failures are returned as a failed response.
    /// </summary>
    public CommandResponse Configure(CommandRequest request)
    {
        try
        {
            if (request.Session != null && !string.IsNullOrWhiteSpace(request.Session.Type))
            {
                OpenRemoteSession(request.Session);
            }

            // Set up the (possibly remote) runspace through Set-Variable so the exact
            // same code path works for local and remote runspaces (SessionStateProxy is
            // not supported on remote runspaces).
            SetGlobalVariable("ErrorActionPreference", "Stop");
            SetGlobalVariable("ProviderData", ConvertToHashtable(request.ProviderData));

            return new CommandResponse { Success = true, OutputData = new(), Error = "" };
        }
        catch (Exception ex)
        {
            return Failure($"Failed to configure PowerShell host: {ex.Message}");
        }
    }

    /// <summary>
    /// Opens a remote PowerShell session via New-PSSession (run in the bootstrap
    /// runspace) and adopts its runspace as the active execution runspace. Supports
    /// WinRM, SSH, and VM guest (PowerShell Direct) transports with the most common
    /// authentication combinations. Throws on failure so <see cref="Configure"/> can
    /// report it.
    /// </summary>
    private void OpenRemoteSession(SessionConfig cfg)
    {
        using var ps = PowerShell.Create();
        ps.Runspace = _bootstrapRunspace;
        ps.AddCommand("New-PSSession");

        PSCredential? credential = null;
        if (!string.IsNullOrEmpty(cfg.Username))
        {
            var secure = new SecureString();
            foreach (var c in cfg.Password ?? "")
            {
                secure.AppendChar(c);
            }
            secure.MakeReadOnly();
            credential = new PSCredential(cfg.Username, secure);
        }

        switch (cfg.Type.ToLowerInvariant())
        {
            case "winrm":
                ps.AddParameter("ComputerName", cfg.Host);
                if (cfg.Port > 0) ps.AddParameter("Port", cfg.Port);
                if (cfg.UseSsl) ps.AddParameter("UseSSL");
                if (!string.IsNullOrEmpty(cfg.Authentication)) ps.AddParameter("Authentication", cfg.Authentication);
                if (!string.IsNullOrEmpty(cfg.ConfigurationName)) ps.AddParameter("ConfigurationName", cfg.ConfigurationName);
                // Certificate auth uses the thumbprint and no credential; otherwise use
                // the username/password credential when supplied.
                if (!string.IsNullOrEmpty(cfg.CertThumbprint)) ps.AddParameter("CertificateThumbprint", cfg.CertThumbprint);
                else if (credential != null) ps.AddParameter("Credential", credential);
                break;

            case "ssh":
                ps.AddParameter("HostName", cfg.Host);
                if (!string.IsNullOrEmpty(cfg.Username)) ps.AddParameter("UserName", cfg.Username);
                if (!string.IsNullOrEmpty(cfg.KeyFile)) ps.AddParameter("KeyFilePath", cfg.KeyFile);
                if (cfg.Port > 0) ps.AddParameter("Port", cfg.Port);
                break;

            case "vmguest":
                if (!string.IsNullOrEmpty(cfg.VmId)) ps.AddParameter("VMId", cfg.VmId);
                else ps.AddParameter("VMName", cfg.VmName);
                if (credential != null) ps.AddParameter("Credential", credential);
                break;

            default:
                throw new InvalidOperationException(
                    $"Unsupported session_type '{cfg.Type}'. Use 'winrm', 'ssh', or 'vmguest'.");
        }

        var result = ps.Invoke();
        if (ps.Streams.Error.Count > 0)
        {
            var msg = string.Join("; ", ps.Streams.Error.Select(e => e.Exception?.Message ?? e.ToString()));
            throw new InvalidOperationException($"New-PSSession failed: {msg}");
        }

        var sessionObj = result.FirstOrDefault(o => o?.BaseObject is PSSession);
        if (sessionObj?.BaseObject is not PSSession session)
        {
            throw new InvalidOperationException("New-PSSession did not return a session object.");
        }

        _session = session;
        _activeRunspace = session.Runspace;
    }

    /// <summary>
    /// Sets a global variable on the active runspace using the Set-Variable cmdlet.
    /// This works identically for local and remote runspaces (complex values such as
    /// the ProviderData hashtable serialize across remoting). Throws on error.
    /// </summary>
    private void SetGlobalVariable(string name, object? value)
    {
        using var ps = PowerShell.Create();
        ps.Runspace = _activeRunspace;
        ps.AddCommand("Set-Variable")
            .AddParameter("Name", name)
            .AddParameter("Value", value)
            .AddParameter("Scope", "Global");
        ps.Invoke();
        if (ps.Streams.Error.Count > 0)
        {
            var msg = string.Join("; ", ps.Streams.Error.Select(e => e.Exception?.Message ?? e.ToString()));
            throw new InvalidOperationException($"Failed to set ${name}: {msg}");
        }
    }

    /// <summary>
    /// The resource CRUD actions whose scripts are run in an isolated child scope.
    /// Lifecycle actions (startup/shutdown/configure) are intentionally excluded so
    /// they can set up modules and persistent globals at the runspace (global) scope.
    /// </summary>
    private static readonly HashSet<string> ScopedActions =
        new(StringComparer.OrdinalIgnoreCase) { "create", "read", "update", "delete" };

    /// <summary>
    /// Wraps the user script with the host-provided <c>param([hashtable]$InputData)</c>
    /// block so the per-call inputs bind as a real parameter, and injects
    /// <c>$Action</c> so the script can branch on the operation.
    ///
    /// For CRUD actions the user body is invoked inside a child scope
    /// (<c>&amp; { param([hashtable]$InputData) ... } -InputData $InputData</c>) so
    /// incidental variables a script creates cannot leak to the next resource. This
    /// works identically on local and remote runspaces. Lifecycle scripts run flat at
    /// the runspace scope so they can establish modules and persistent globals. The
    /// action is a controlled enum, so embedding it as a single-quoted literal is safe.
    ///
    /// A resource script may declare its own <c>param</c> block that already binds
    /// <c>$InputData</c>; in that case we do not inject a second param block (which
    /// would be a syntax error) and leave the user's block as-is - it need not specify
    /// a type and may declare additional (non-mandatory) parameters in any order.
    /// The input is bound by name (<c>-InputData</c>), not positionally, so it lands on
    /// the right parameter regardless of declaration order; other parameters keep their
    /// defaults. When the host supplies the param block it always types it as
    /// <c>[hashtable]</c> to match what <see cref="ConvertToHashtable"/> passes in.
    /// </summary>
    private static string BuildScript(string? action, string userScript)
    {
        var actionLiteral = (action ?? "").Replace("'", "''");
        bool isCrud = action != null && ScopedActions.Contains(action);

        if (isCrud)
        {
            // Only add our typed param block when the user script doesn't already
            // declare $InputData itself - two param blocks in one scope won't parse.
            var innerParam = ScriptDeclaresInputData(userScript)
                ? ""
                : "param([hashtable]$InputData)\n";

            return
                "param([hashtable]$InputData)\n" +
                $"$Action = '{actionLiteral}'\n" +
                "& {\n" +
                innerParam +
                userScript + "\n" +
                "} -InputData $InputData\n";
        }

        return
            "param([hashtable]$InputData)\n" +
            $"$Action = '{actionLiteral}'\n" +
            userScript;
    }

    /// <summary>
    /// Uses the PowerShell AST to determine whether the user script already declares a
    /// top-level <c>param</c> block that binds an <c>$InputData</c> parameter. When it
    /// does, the host must not inject its own param block for the same scope.
    /// </summary>
    private static bool ScriptDeclaresInputData(string userScript)
    {
        var ast = Parser.ParseInput(userScript, out _, out _);
        var paramBlock = ast.ParamBlock;
        if (paramBlock == null) return false;

        return paramBlock.Parameters.Any(p => string.Equals(
            p.Name.VariablePath.UserPath, "InputData", StringComparison.OrdinalIgnoreCase));
    }

    /// <summary>
    /// Writes the script's information/warning/verbose/debug/error streams to stderr
    /// for host-side diagnostics. The success stream is intentionally excluded - it is
    /// the result and is converted into <see cref="CommandResponse.OutputData"/>.
    /// </summary>
    private static void LogStreams(PowerShell ps, string? action)
    {
        var prefix = $"[pshost:{action}]";
        foreach (var r in ps.Streams.Information)
            Console.Error.WriteLine($"{prefix} INFO: {r}");
        foreach (var r in ps.Streams.Warning)
            Console.Error.WriteLine($"{prefix} WARN: {r.Message}");
        foreach (var r in ps.Streams.Verbose)
            Console.Error.WriteLine($"{prefix} VERBOSE: {r.Message}");
        foreach (var r in ps.Streams.Debug)
            Console.Error.WriteLine($"{prefix} DEBUG: {r.Message}");
        foreach (var r in ps.Streams.Error)
            Console.Error.WriteLine($"{prefix} ERROR: {r.Exception?.Message ?? r.ToString()}");
    }

    /// <summary>
    /// Converts the single success-stream object the script emitted into a
    /// Dictionary suitable for JSON serialization. Handles both hashtable output
    /// (<c>@{ ... }</c>) and PSCustomObject output (<c>[PSCustomObject]@{ ... }</c>).
    /// </summary>
    private static Dictionary<string, object?> ConvertResultObject(PSObject result)
    {
        if (result.BaseObject is IDictionary dict)
        {
            return ConvertIDictionary(dict);
        }
        return ConvertFromPSObject(result);
    }

    private static CommandResponse Failure(string error) =>
        new CommandResponse
        {
            Success = false,
            OutputData = new Dictionary<string, object?>(),
            Error = error
        };

    /// <summary>
    /// Converts a Dictionary&lt;string, object?&gt; from JSON deserialization into a Hashtable
    /// that PowerShell scripts can work with naturally.
    /// </summary>
    private static Hashtable ConvertToHashtable(Dictionary<string, object?>? dict)
    {
        var ht = new Hashtable(StringComparer.OrdinalIgnoreCase);
        if (dict == null) return ht;

        foreach (var kvp in dict)
        {
            ht[kvp.Key] = ConvertValueToPS(kvp.Value);
        }
        return ht;
    }

    /// <summary>
    /// Recursively converts JSON-deserialized values into PowerShell-friendly types.
    /// </summary>
    private static object? ConvertValueToPS(object? value)
    {
        if (value is Dictionary<string, object?> dict)
        {
            return ConvertToHashtable(dict);
        }
        if (value is System.Text.Json.JsonElement jsonElement)
        {
            return ConvertJsonElement(jsonElement);
        }
        return value;
    }

    private static object? ConvertJsonElement(System.Text.Json.JsonElement element)
    {
        switch (element.ValueKind)
        {
            case System.Text.Json.JsonValueKind.Object:
                var ht = new Hashtable(StringComparer.OrdinalIgnoreCase);
                foreach (var prop in element.EnumerateObject())
                {
                    ht[prop.Name] = ConvertJsonElement(prop.Value);
                }
                return ht;
            case System.Text.Json.JsonValueKind.Array:
                var list = new List<object?>();
                foreach (var item in element.EnumerateArray())
                {
                    list.Add(ConvertJsonElement(item));
                }
                return list.ToArray();
            case System.Text.Json.JsonValueKind.String:
                return element.GetString();
            case System.Text.Json.JsonValueKind.Number:
                if (element.TryGetInt64(out long l)) return l;
                return element.GetDouble();
            case System.Text.Json.JsonValueKind.True:
                return true;
            case System.Text.Json.JsonValueKind.False:
                return false;
            case System.Text.Json.JsonValueKind.Null:
                return null;
            default:
                return element.ToString();
        }
    }

    /// <summary>
    /// Converts a PowerShell variable value (Hashtable, PSObject, etc.) into a
    /// Dictionary&lt;string, object?&gt; suitable for JSON serialization.
    /// </summary>
    private static Dictionary<string, object?> ConvertFromPSObject(object? value)
    {
        var result = new Dictionary<string, object?>(StringComparer.OrdinalIgnoreCase);

        switch (value)
        {
            case Hashtable ht:
                foreach (DictionaryEntry entry in ht)
                {
                    result[entry.Key.ToString()!] = ConvertOutputValue(entry.Value);
                }
                break;
            case PSObject pso:
                foreach (var prop in pso.Properties)
                {
                    try
                    {
                        result[prop.Name] = ConvertOutputValue(prop.Value);
                    }
                    catch
                    {
                        // Skip properties that can't be read
                    }
                }
                break;
        }

        return result;
    }

    /// <summary>
    /// Recursively converts PowerShell output values to JSON-serializable types.
    /// </summary>
    private static object? ConvertOutputValue(object? value)
    {
        if (value == null) return null;

        // Unwrap PSObject
        if (value is PSObject pso)
        {
            value = pso.BaseObject;
        }

        return value switch
        {
            Hashtable ht => ConvertFromPSObject(ht),
            IDictionary dict => ConvertIDictionary(dict),
            string s => s,
            bool => value,
            int or long or float or double or decimal => value,
            IList list => list.Cast<object?>().Select(ConvertOutputValue).ToArray(),
            _ => value.ToString()
        };
    }

    private static Dictionary<string, object?> ConvertIDictionary(IDictionary dict)
    {
        var result = new Dictionary<string, object?>(StringComparer.OrdinalIgnoreCase);
        foreach (DictionaryEntry entry in dict)
        {
            result[entry.Key.ToString()!] = ConvertOutputValue(entry.Value);
        }
        return result;
    }

    /// <summary>
    /// Closes the remote session (if one was opened) and disposes the runspaces.
    /// Idempotent. The shutdown script, when present, has already run via a normal
    /// command before Dispose, so removing the session here is the final teardown step
    /// - the equivalent of exiting the session.
    /// </summary>
    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;

        if (_session != null)
        {
            try
            {
                using var ps = PowerShell.Create();
                ps.Runspace = _bootstrapRunspace;
                ps.AddCommand("Remove-PSSession").AddParameter("Session", _session);
                ps.Invoke();
            }
            catch
            {
                // Best effort: the remote end may already be gone.
            }
            _session = null;
        }

        _bootstrapRunspace.Close();
        _bootstrapRunspace.Dispose();
    }
}

/// <summary>
/// A single command from the provider. Property names map (via snake_case JSON) to
/// the Go PSCommand struct: action, script, input_data, provider_data, session.
/// </summary>
public class CommandRequest
{
    /// <summary>The operation being performed, e.g. "configure"/"create"/"read"/"update"/"delete".</summary>
    public string? Action { get; set; }

    /// <summary>The PowerShell script to run. Reads the $InputData parameter, emits one result object.</summary>
    public string? Script { get; set; }

    /// <summary>Inputs exposed to the script as the bound $InputData parameter.</summary>
    public Dictionary<string, object?>? InputData { get; set; }

    /// <summary>Connection arguments exposed to scripts as $global:ProviderData (configure only).</summary>
    public Dictionary<string, object?>? ProviderData { get; set; }

    /// <summary>Optional remote session details (configure only).</summary>
    public SessionConfig? Session { get; set; }
}

/// <summary>
/// Remote PowerShell session details. Field names map to the Go PSSessionConfig
/// struct via the explicit JSON property names below.
/// </summary>
public class SessionConfig
{
    [JsonPropertyName("type")] public string Type { get; set; } = "";
    [JsonPropertyName("host")] public string? Host { get; set; }
    [JsonPropertyName("port")] public int Port { get; set; }
    [JsonPropertyName("username")] public string? Username { get; set; }
    [JsonPropertyName("password")] public string? Password { get; set; }
    [JsonPropertyName("use_ssl")] public bool UseSsl { get; set; }
    [JsonPropertyName("authentication")] public string? Authentication { get; set; }
    [JsonPropertyName("cert_thumbprint")] public string? CertThumbprint { get; set; }
    [JsonPropertyName("configuration_name")] public string? ConfigurationName { get; set; }
    [JsonPropertyName("key_file")] public string? KeyFile { get; set; }
    [JsonPropertyName("vm_name")] public string? VmName { get; set; }
    [JsonPropertyName("vm_id")] public string? VmId { get; set; }
}

/// <summary>
/// The result of running a command, serialized back to the provider as JSON.
/// Maps to the Go PSResponse struct: success, output_data, error.
/// </summary>
public class CommandResponse
{
    /// <summary>True if the script ran without terminating or non-terminating errors.</summary>
    public bool Success { get; set; }

    /// <summary>The single object the script emitted to the success stream, as a map.</summary>
    public Dictionary<string, object?> OutputData { get; set; } = new();

    /// <summary>Error message when <see cref="Success"/> is false; empty otherwise.</summary>
    public string Error { get; set; } = "";
}
