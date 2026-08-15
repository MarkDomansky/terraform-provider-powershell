// =============================================================================
// pshost - PowerShell sidecar host
//
// This executable is the "sidecar" process launched by the Go-based Terraform
// provider (see internal/provider/ps_manager.go). It hosts a single, long-lived
// PowerShell runspace and executes scripts on demand, communicating with the Go
// process over stdin/stdout using a simple line-delimited, marker-framed JSON
// protocol.
//
// Why a separate process? It lets the provider run real PowerShell without
// depending on a system PowerShell install or the .NET runtime being present on
// the machine: the project publishes a self-contained, single-file binary per
// platform (see PSHost.csproj). Keeping one runspace alive for the whole process
// also means any globals a script creates survive across every resource operation
// in a single Terraform run.
//
// Wire protocol (one command/response per exchange):
//
//   Go -> pshost (a command):
//       ###PS_CMD_START###
//       { ...CommandRequest as JSON... }      (may span multiple lines)
//       ###PS_CMD_END###
//
//   pshost -> Go (a response):
//       ###PS_RSP_START###
//       { ...CommandResponse as JSON... }
//       ###PS_RSP_END###
//
// There is no readiness banner: pshost opens its runspace synchronously before it
// begins reading stdin, so the first command the caller sends (always a "configure"
// command) blocks on its response until the runspace is up - that response is the
// de-facto ready signal. The "configure" action seeds $global:ProviderData with the
// provider's connection arguments and, when session details are supplied, opens a
// remote PowerShell session that every later script runs in. The literal line "exit"
// (or EOF on stdin) ends the loop and shuts the process down.
// =============================================================================

using System.Diagnostics.CodeAnalysis;
using System.Text.Json;
using System.Text.Json.Serialization;
using PSHost;

// Framing markers. These strings MUST stay in sync with the constants in
// internal/provider/ps_manager.go and the integration tests; they delimit
// commands and responses on the stdin/stdout streams.
const string CmdStartMarker = "###PS_CMD_START###";
const string CmdEndMarker = "###PS_CMD_END###";
const string RspStartMarker = "###PS_RSP_START###";
const string RspEndMarker = "###PS_RSP_END###";

// JSON settings shared by request deserialization and response serialization.
// snake_case matches the Go struct tags (action, input_data, provider_state, ...).
// An explicit DefaultJsonTypeInfoResolver is used so reflection-based
// (de)serialization keeps working even though the app is published with the
// single-file/IL settings in PSHost.csproj.
#pragma warning disable IL2026 // Trimmer roots preserve required types
var jsonOptions = new JsonSerializerOptions
{
    PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
    DefaultIgnoreCondition = JsonIgnoreCondition.Never,
    WriteIndented = false,
    TypeInfoResolver = new System.Text.Json.Serialization.Metadata.DefaultJsonTypeInfoResolver()
};
#pragma warning restore IL2026

// Open the runspace once, up front. It is disposed automatically when this
// process exits (the `using` declaration), closing the PowerShell runspace.
using var runspaceManager = new RunspaceManager();

// Main command loop: read one framed command, execute it, write one framed
// response, repeat. The loop exits on "exit", EOF, or a broken stdin pipe.
while (true)
{
    string? line;
    try
    {
        line = Console.In.ReadLine();
    }
    catch
    {
        // Stdin was closed underneath us (caller went away) - stop.
        break;
    }

    if (line == null)
    {
        // EOF on stdin - the caller closed the pipe, so shut down.
        break;
    }

    line = line.Trim();

    if (line == "exit")
    {
        // Graceful shutdown requested by the caller.
        break;
    }

    // Ignore anything that is not the start of a command frame. This makes the
    // protocol resilient to stray blank lines or banner output on the channel.
    if (line != CmdStartMarker)
    {
        continue;
    }

    // Read every line up to the end marker. The JSON payload may be split across
    // multiple lines, so we accumulate and re-join them. EOF before the end
    // marker means the caller died mid-command; bail out to shutdown.
    var jsonLines = new List<string>();
    while (true)
    {
        var cmdLine = Console.In.ReadLine();
        if (cmdLine == null) goto shutdown;
        if (cmdLine.Trim() == CmdEndMarker) break;
        jsonLines.Add(cmdLine);
    }

    var jsonPayload = string.Join("\n", jsonLines);

    // Deserialize and execute. Any failure (bad JSON, null request, or a thrown
    // exception) is turned into a CommandResponse with Success = false rather
    // than crashing the host, so a single bad command never kills the sidecar.
    CommandResponse response;
    try
    {
        var request = DeserializeRequest(jsonPayload, jsonOptions);
        if (request == null)
        {
            response = new CommandResponse
            {
                Success = false,
                Error = "Failed to deserialize command: null result"
            };
        }
        else
        {
            response = runspaceManager.Execute(request);
        }
    }
    catch (Exception ex)
    {
        response = new CommandResponse
        {
            Success = false,
            Error = $"Failed to parse command JSON: {ex.Message}"
        };
    }

    // Write the framed response and flush so the caller sees it immediately.
    var responseJson = SerializeResponse(response, jsonOptions);
    Console.Out.WriteLine(RspStartMarker);
    Console.Out.WriteLine(responseJson);
    Console.Out.WriteLine(RspEndMarker);
    Console.Out.Flush();
}

shutdown:;

// Local serialization helpers. These are wrapped in their own methods so the
// trimming-suppression attribute can be applied precisely: PowerShell assemblies
// are preserved via TrimmerRootAssembly, so reflection-based JSON is safe here.
// Suppress trimming warnings - PowerShell assemblies are preserved via TrimmerRootAssembly
[UnconditionalSuppressMessage("Trimming", "IL2026")]
static CommandRequest? DeserializeRequest(string json, JsonSerializerOptions options) =>
    JsonSerializer.Deserialize<CommandRequest>(json, options);

[UnconditionalSuppressMessage("Trimming", "IL2026")]
static string SerializeResponse(CommandResponse response, JsonSerializerOptions options) =>
    JsonSerializer.Serialize(response, options);
