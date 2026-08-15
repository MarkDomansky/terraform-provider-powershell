using System.Diagnostics;
using System.Text.Json;
using System.Text.Json.Serialization;
using Xunit;

namespace PSHost.Tests;

/// <summary>
/// Integration tests that exercise the full sidecar process via stdin/stdout,
/// matching how Go's PSManager communicates with the sidecar.
/// </summary>
public class ProtocolIntegrationTests : IDisposable
{
    private const string CmdStartMarker = "###PS_CMD_START###";
    private const string CmdEndMarker = "###PS_CMD_END###";
    private const string RspStartMarker = "###PS_RSP_START###";
    private const string RspEndMarker = "###PS_RSP_END###";

    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        DefaultIgnoreCondition = JsonIgnoreCondition.Never,
        WriteIndented = false
    };

    private readonly Process _process;
    private readonly StreamWriter _stdin;
    private readonly StreamReader _stdout;

    public ProtocolIntegrationTests()
    {
        // Find the PSHost project and run it via dotnet run
        var projectDir = FindProjectDir();

        var psi = new ProcessStartInfo
        {
            FileName = "dotnet",
            Arguments = $"run --project \"{projectDir}\"",
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true
        };

        _process = Process.Start(psi) ?? throw new Exception("Failed to start pshost process");
        _stdin = _process.StandardInput;
        _stdout = _process.StandardOutput;

        // No readiness banner: the sidecar opens its runspace before reading stdin, so
        // the first SendCommand simply blocks on its response until the host is ready.
    }

    public void Dispose()
    {
        try
        {
            _stdin.WriteLine("exit");
            _stdin.Flush();
            _stdin.Close();
        }
        catch { }

        if (!_process.WaitForExit(5000))
        {
            _process.Kill();
        }
        _process.Dispose();
    }

    private static string FindProjectDir()
    {
        // Walk up from test assembly location to find the PSHost project
        var dir = AppContext.BaseDirectory;
        while (dir != null)
        {
            var candidate = Path.Combine(dir, "csharp", "PSHost");
            if (Directory.Exists(candidate) && File.Exists(Path.Combine(candidate, "PSHost.csproj")))
                return candidate;
            dir = Path.GetDirectoryName(dir);
        }
        throw new Exception("Could not find PSHost project directory");
    }

    private CommandResponse SendCommand(CommandRequest request)
    {
        var json = JsonSerializer.Serialize(request, JsonOptions);
        _stdin.WriteLine(CmdStartMarker);
        _stdin.WriteLine(json);
        _stdin.WriteLine(CmdEndMarker);
        _stdin.Flush();

        // Read response
        var jsonLines = new List<string>();
        bool inResponse = false;

        while (true)
        {
            var line = _stdout.ReadLine();
            if (line == null) throw new Exception("Process exited without response");

            var trimmed = line.Trim();
            if (trimmed == RspStartMarker)
            {
                inResponse = true;
                jsonLines.Clear();
                continue;
            }
            if (trimmed == RspEndMarker && inResponse)
            {
                var payload = string.Join("\n", jsonLines);
                return JsonSerializer.Deserialize<CommandResponse>(payload, JsonOptions)
                    ?? throw new Exception("Null response");
            }
            if (inResponse)
            {
                jsonLines.Add(line);
            }
        }
    }

    [Fact]
    public void Protocol_BasicExecution()
    {
        var response = SendCommand(new CommandRequest
        {
            Action = "create",
            Script = "[PSCustomObject]@{ id = 'test-1'; status = 'ok' }"
        });

        Assert.True(response.Success);
        Assert.Equal("test-1", response.OutputData["id"]?.ToString());
        Assert.Equal("ok", response.OutputData["status"]?.ToString());
    }

    [Fact]
    public void Protocol_InputDataPassing()
    {
        var response = SendCommand(new CommandRequest
        {
            Action = "create",
            Script = @"[PSCustomObject]@{ id = $InputData.name }",
            InputData = new Dictionary<string, object?> { ["name"] = "hello" }
        });

        Assert.True(response.Success);
        Assert.Equal("hello", response.OutputData["id"]?.ToString());
    }

    [Fact]
    public void Protocol_ErrorHandling()
    {
        var response = SendCommand(new CommandRequest
        {
            Action = "create",
            Script = @"throw 'test error'"
        });

        Assert.False(response.Success);
        Assert.Contains("test error", response.Error);
    }

    [Fact]
    public void Protocol_GlobalPersists()
    {
        // A global created in one call is visible to a later call (persistent runspace).
        var r1 = SendCommand(new CommandRequest
        {
            Action = "startup",
            Script = @"$global:MyKey = 'value123'"
        });
        Assert.True(r1.Success);

        var r2 = SendCommand(new CommandRequest
        {
            Action = "read",
            Script = @"[PSCustomObject]@{ key = $global:MyKey }"
        });
        Assert.True(r2.Success);
        Assert.Equal("value123", r2.OutputData["key"]?.ToString());
    }

    [Fact]
    public void Protocol_Configure_SeedsProviderData()
    {
        // The configure command (over the wire) seeds $global:ProviderData.
        var configure = SendCommand(new CommandRequest
        {
            Action = "configure",
            ProviderData = new Dictionary<string, object?>
            {
                ["server"] = "host01",
                ["Data"] = new Dictionary<string, object?> { ["env"] = "prod" }
            }
        });
        Assert.True(configure.Success, configure.Error);

        var read = SendCommand(new CommandRequest
        {
            Action = "read",
            Script = @"[PSCustomObject]@{ server = $global:ProviderData.server; env = $global:ProviderData.Data.env }"
        });
        Assert.True(read.Success, read.Error);
        Assert.Equal("host01", read.OutputData["server"]?.ToString());
        Assert.Equal("prod", read.OutputData["env"]?.ToString());
    }

    [Fact]
    public void Protocol_MultipleResourcesCrudCycle()
    {
        // Create resource A
        var createA = SendCommand(new CommandRequest
        {
            Action = "create",
            Script = @"[PSCustomObject]@{ id = 'a'; val = $InputData.v }",
            InputData = new Dictionary<string, object?> { ["v"] = "alpha" }
        });
        Assert.True(createA.Success);

        // Create resource B
        var createB = SendCommand(new CommandRequest
        {
            Action = "create",
            Script = @"[PSCustomObject]@{ id = 'b'; val = $InputData.v }",
            InputData = new Dictionary<string, object?> { ["v"] = "beta" }
        });
        Assert.True(createB.Success);

        // Read A
        var readA = SendCommand(new CommandRequest
        {
            Action = "read",
            Script = @"[PSCustomObject]@{ id = 'a'; val = 'alpha' }",
            InputData = new Dictionary<string, object?> { ["id"] = "a" }
        });
        Assert.True(readA.Success);

        // Update B
        var updateB = SendCommand(new CommandRequest
        {
            Action = "update",
            Script = @"[PSCustomObject]@{ id = 'b'; val = 'beta-v2' }",
            InputData = new Dictionary<string, object?> { ["id"] = "b" }
        });
        Assert.True(updateB.Success);

        // Delete both
        var deleteA = SendCommand(new CommandRequest
        {
            Action = "delete",
            Script = "# deleted"
        });
        Assert.True(deleteA.Success);

        var deleteB = SendCommand(new CommandRequest
        {
            Action = "delete",
            Script = "# deleted"
        });
        Assert.True(deleteB.Success);
    }

    [Fact]
    public void Protocol_IncidentalVariable_DoesNotLeakBetweenResources()
    {
        // Resource A leaves a bare variable behind in its CRUD script.
        var createA = SendCommand(new CommandRequest
        {
            Action = "create",
            Script = @"$leaked = 'from-a'; [PSCustomObject]@{ id = 'a' }"
        });
        Assert.True(createA.Success);

        // Resource B must not see it, because CRUD scripts run isolated.
        var createB = SendCommand(new CommandRequest
        {
            Action = "create",
            Script = @"[PSCustomObject]@{ id = 'b'; seen = ""$leaked"" }"
        });
        Assert.True(createB.Success);
        Assert.Equal("", createB.OutputData["seen"]?.ToString());
    }

    [Fact]
    public void Protocol_FailedScript_ReportsError()
    {
        var failed = SendCommand(new CommandRequest
        {
            Action = "update",
            Script = @"throw 'boom'"
        });
        Assert.False(failed.Success);
        Assert.Contains("boom", failed.Error);
    }
}
