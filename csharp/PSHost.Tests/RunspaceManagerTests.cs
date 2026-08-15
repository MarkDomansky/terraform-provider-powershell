using Xunit;

namespace PSHost.Tests;

public class RunspaceManagerTests : IDisposable
{
    private readonly RunspaceManager _manager;

    public RunspaceManagerTests()
    {
        _manager = new RunspaceManager();
    }

    public void Dispose()
    {
        _manager.Dispose();
    }

    [Fact]
    public void Execute_SimpleScript_ReturnsOutputData()
    {
        var request = new CommandRequest
        {
            Action = "create",
            Script = "[PSCustomObject]@{ id = 'test-123'; status = 'created' }"
        };

        var response = _manager.Execute(request);

        Assert.True(response.Success);
        Assert.Equal("test-123", response.OutputData["id"]?.ToString());
        Assert.Equal("created", response.OutputData["status"]?.ToString());
    }

    [Fact]
    public void Execute_WithInputData_PassesDataToScript()
    {
        var request = new CommandRequest
        {
            Action = "create",
            Script = @"[PSCustomObject]@{ id = $InputData.name; location = $InputData.location; combined = ""$($InputData.name)-$($InputData.location)"" }",
            InputData = new Dictionary<string, object?>
            {
                ["name"] = "my-resource",
                ["location"] = "eastus"
            }
        };

        var response = _manager.Execute(request);

        Assert.True(response.Success);
        Assert.Equal("my-resource", response.OutputData["id"]?.ToString());
        Assert.Equal("my-resource-eastus", response.OutputData["combined"]?.ToString());
    }

    [Fact]
    public void Execute_ScriptWithOwnInputDataParam_UsesUserParamBlock()
    {
        // A resource script may declare its own param block binding $InputData. The
        // host must not inject a second param block (that wouldn't parse); the user's
        // block need not specify a type and still receives the hashtable.
        var request = new CommandRequest
        {
            Action = "create",
            Script = "param($InputData)\n[PSCustomObject]@{ id = $InputData.name }",
            InputData = new Dictionary<string, object?> { ["name"] = "user-param" }
        };

        var response = _manager.Execute(request);

        Assert.True(response.Success);
        Assert.Equal("user-param", response.OutputData["id"]?.ToString());
    }

    [Fact]
    public void Execute_ScriptWithOwnTypedInputDataParam_IsRespected()
    {
        // A user param block that already types $InputData must not be duplicated.
        var request = new CommandRequest
        {
            Action = "update",
            Script = "param([hashtable]$InputData)\n[PSCustomObject]@{ id = $InputData.id }",
            InputData = new Dictionary<string, object?> { ["id"] = "typed-param" }
        };

        var response = _manager.Execute(request);

        Assert.True(response.Success);
        Assert.Equal("typed-param", response.OutputData["id"]?.ToString());
    }

    [Fact]
    public void Execute_ScriptWithExtraParamsInputDataNotFirst_BindsByName()
    {
        // A user param block may declare other (non-mandatory) params in any order.
        // The host binds -InputData by name, so it lands correctly even when it isn't
        // the first parameter; the extra param keeps its default.
        var request = new CommandRequest
        {
            Action = "create",
            Script = "param($Other = 'dflt', [hashtable]$InputData)\n"
                   + "[PSCustomObject]@{ id = $InputData.name; other = $Other }",
            InputData = new Dictionary<string, object?> { ["name"] = "by-name" }
        };

        var response = _manager.Execute(request);

        Assert.True(response.Success);
        Assert.Equal("by-name", response.OutputData["id"]?.ToString());
        Assert.Equal("dflt", response.OutputData["other"]?.ToString());
    }

    [Fact]
    public void Execute_ScriptWithParamAfterComments_UsesUserParamBlock()
    {
        // Comments and attributes before the param block must not defeat detection.
        var request = new CommandRequest
        {
            Action = "read",
            Script = "<# doc #>\n[CmdletBinding()]\nparam ( $InputData )\n"
                   + "[PSCustomObject]@{ id = $InputData.id }",
            InputData = new Dictionary<string, object?> { ["id"] = "commented" }
        };

        var response = _manager.Execute(request);

        Assert.True(response.Success);
        Assert.Equal("commented", response.OutputData["id"]?.ToString());
    }

    [Fact]
    public void Execute_ScriptError_ReturnsFailure()
    {
        var request = new CommandRequest
        {
            Action = "create",
            Script = @"throw 'something went wrong'"
        };

        var response = _manager.Execute(request);

        Assert.False(response.Success);
        Assert.NotEmpty(response.Error);
        Assert.Contains("something went wrong", response.Error);
    }

    [Fact]
    public void Execute_NonTerminatingError_ReturnsFailure()
    {
        // A record on the error stream is a failure even when the script does not throw.
        var request = new CommandRequest
        {
            Action = "create",
            Script = @"$ErrorActionPreference = 'Continue'; Write-Error 'soft failure'; [PSCustomObject]@{ id = 'x' }"
        };

        var response = _manager.Execute(request);

        Assert.False(response.Success);
        Assert.Contains("soft failure", response.Error);
    }

    [Fact]
    public void Execute_MultipleOutputObjects_ReturnsFailure()
    {
        // The contract is exactly one output object; emitting two is a failure.
        var request = new CommandRequest
        {
            Action = "create",
            Script = @"[PSCustomObject]@{ id = 'one' }; [PSCustomObject]@{ id = 'two' }"
        };

        var response = _manager.Execute(request);

        Assert.False(response.Success);
        Assert.Contains("one object", response.Error);
    }

    [Fact]
    public void Execute_HashtableOutput_IsConverted()
    {
        // A bare hashtable emitted to the output stream is converted just like a PSCustomObject.
        var response = _manager.Execute(new CommandRequest
        {
            Action = "create",
            Script = @"@{ id = 'ht-1'; value = 42 }"
        });

        Assert.True(response.Success);
        Assert.Equal("ht-1", response.OutputData["id"]?.ToString());
        Assert.Equal("42", response.OutputData["value"]?.ToString());
    }

    [Fact]
    public void Execute_GlobalVariable_PersistsAcrossCalls()
    {
        // The runspace is persistent, so a global a script creates in one call is
        // visible to a later call. (There is no provider-managed state bag.)
        var response1 = _manager.Execute(new CommandRequest
        {
            Action = "startup",
            Script = @"$global:MyToken = 'abc123'"
        });
        Assert.True(response1.Success);

        var response2 = _manager.Execute(new CommandRequest
        {
            Action = "read",
            Script = @"[PSCustomObject]@{ token = $global:MyToken }"
        });

        Assert.True(response2.Success);
        Assert.Equal("abc123", response2.OutputData["token"]?.ToString());
    }

    [Fact]
    public void Configure_SeedsProviderData_ReadableByLaterScript()
    {
        // The one-time configure command (no session) seeds $global:ProviderData and
        // leaves execution local.
        var configure = _manager.Execute(new CommandRequest
        {
            Action = "configure",
            ProviderData = new Dictionary<string, object?>
            {
                ["server"] = "db01.example.com",
                ["username"] = "svc-deploy",
                ["password"] = "s3cret",
                ["Data"] = new Dictionary<string, object?>
                {
                    ["environment"] = "prod"
                }
            }
        });
        Assert.True(configure.Success, configure.Error);

        var response = _manager.Execute(new CommandRequest
        {
            Action = "read",
            Script = @"
[PSCustomObject]@{
    server      = $global:ProviderData.server
    username    = $global:ProviderData.username
    password    = $global:ProviderData.password
    environment = $global:ProviderData.Data.environment
}
"
        });

        Assert.True(response.Success, response.Error);
        Assert.Equal("db01.example.com", response.OutputData["server"]?.ToString());
        Assert.Equal("svc-deploy", response.OutputData["username"]?.ToString());
        Assert.Equal("s3cret", response.OutputData["password"]?.ToString());
        Assert.Equal("prod", response.OutputData["environment"]?.ToString());
    }

    [Fact]
    public void Configure_WithoutProviderData_LeavesEmptyHashtable()
    {
        var configure = _manager.Execute(new CommandRequest { Action = "configure" });
        Assert.True(configure.Success, configure.Error);

        var response = _manager.Execute(new CommandRequest
        {
            Action = "read",
            Script = @"[PSCustomObject]@{ count = $global:ProviderData.Keys.Count }"
        });

        Assert.True(response.Success, response.Error);
        Assert.Equal("0", response.OutputData["count"]?.ToString());
    }

    [Fact]
    public void Execute_MultipleSequentialCommands_AllSucceed()
    {
        for (int i = 0; i < 5; i++)
        {
            var request = new CommandRequest
            {
                Action = "create",
                Script = @"[PSCustomObject]@{ id = ""item-$($InputData.index)"" }",
                InputData = new Dictionary<string, object?>
                {
                    ["index"] = i
                }
            };

            var response = _manager.Execute(request);

            Assert.True(response.Success, $"Command {i} failed: {response.Error}");
        }
    }

    [Fact]
    public void Execute_ComplexNestedData_RoundTrips()
    {
        var request = new CommandRequest
        {
            Action = "create",
            Script = @"
[PSCustomObject]@{
    id = 'complex-1'
    name = $InputData.name
    tag_count = $InputData.tags.Count
    doubled_count = [int]$InputData.count * 2
}
",
            InputData = new Dictionary<string, object?>
            {
                ["name"] = "test",
                ["tags"] = new Dictionary<string, object?>
                {
                    ["env"] = "dev",
                    ["team"] = "platform"
                },
                ["count"] = 42
            }
        };

        var response = _manager.Execute(request);

        Assert.True(response.Success, $"Script failed: {response.Error}");
        Assert.Equal("test", response.OutputData["name"]?.ToString());
    }

    [Fact]
    public void Execute_ActionVariable_IsSet()
    {
        var request = new CommandRequest
        {
            Action = "delete",
            Script = @"[PSCustomObject]@{ action_was = $Action }"
        };

        var response = _manager.Execute(request);

        Assert.True(response.Success);
        Assert.Equal("delete", response.OutputData["action_was"]?.ToString());
    }

    [Fact]
    public void Execute_EmptyOutput_ReturnsEmpty()
    {
        var request = new CommandRequest
        {
            Action = "read",
            Script = "# resource not found, emit nothing"
        };

        var response = _manager.Execute(request);

        Assert.True(response.Success);
        Assert.Empty(response.OutputData);
    }

    [Fact]
    public void Execute_CmdletErrorAction_ReturnsFailure()
    {
        var request = new CommandRequest
        {
            Action = "read",
            Script = @"Get-Item -Path '/nonexistent/path/that/does/not/exist' -ErrorAction Stop"
        };

        var response = _manager.Execute(request);

        Assert.False(response.Success);
        Assert.NotEmpty(response.Error);
    }

    [Fact]
    public void Execute_MultipleResourcesCrudCycle()
    {
        // Simulate two independent resources going through create, read, update, delete
        // through the same RunspaceManager (same runspace)

        // Resource A: create
        var createA = _manager.Execute(new CommandRequest
        {
            Action = "create",
            Script = @"[PSCustomObject]@{ id = 'res-a'; value = $InputData.val }",
            InputData = new Dictionary<string, object?> { ["val"] = "alpha" }
        });
        Assert.True(createA.Success);
        Assert.Equal("res-a", createA.OutputData["id"]?.ToString());

        // Resource B: create
        var createB = _manager.Execute(new CommandRequest
        {
            Action = "create",
            Script = @"[PSCustomObject]@{ id = 'res-b'; value = $InputData.val }",
            InputData = new Dictionary<string, object?> { ["val"] = "beta" }
        });
        Assert.True(createB.Success);
        Assert.Equal("res-b", createB.OutputData["id"]?.ToString());

        // Resource A: read
        var readA = _manager.Execute(new CommandRequest
        {
            Action = "read",
            Script = @"[PSCustomObject]@{ id = $InputData.id; value = 'alpha' }",
            InputData = new Dictionary<string, object?> { ["id"] = "res-a" }
        });
        Assert.True(readA.Success);
        Assert.Equal("res-a", readA.OutputData["id"]?.ToString());

        // Resource B: update
        var updateB = _manager.Execute(new CommandRequest
        {
            Action = "update",
            Script = @"[PSCustomObject]@{ id = $InputData.id; value = 'beta-updated' }",
            InputData = new Dictionary<string, object?> { ["id"] = "res-b" }
        });
        Assert.True(updateB.Success);
        Assert.Equal("beta-updated", updateB.OutputData["value"]?.ToString());

        // Resource A: delete
        var deleteA = _manager.Execute(new CommandRequest
        {
            Action = "delete",
            Script = "# deleted",
            InputData = new Dictionary<string, object?> { ["id"] = "res-a" }
        });
        Assert.True(deleteA.Success);

        // Resource B: delete
        var deleteB = _manager.Execute(new CommandRequest
        {
            Action = "delete",
            Script = "# deleted",
            InputData = new Dictionary<string, object?> { ["id"] = "res-b" }
        });
        Assert.True(deleteB.Success);
    }

    [Fact]
    public void Execute_IncidentalGlobals_DoNotLeakBetweenCrudCalls()
    {
        // Resource A leaves a bare (unscoped) variable behind. Because CRUD scripts
        // run in an isolated child scope, it must NOT be visible to resource B.
        var createA = _manager.Execute(new CommandRequest
        {
            Action = "create",
            Script = @"$leakedSecret = 'from-resource-a'; [PSCustomObject]@{ id = 'res-a' }"
        });
        Assert.True(createA.Success);

        var createB = _manager.Execute(new CommandRequest
        {
            Action = "create",
            // If the variable leaked, $leakedSecret would resolve; isolation makes it $null.
            Script = @"[PSCustomObject]@{ id = 'res-b'; seen = ""$leakedSecret"" }"
        });
        Assert.True(createB.Success);
        Assert.Equal("", createB.OutputData["seen"]?.ToString());
    }

    [Fact]
    public void Execute_GlobalCreatedByScript_IsNotRolledBackOnLaterFailure()
    {
        // Globals a script creates persist for the life of the runspace; there is no
        // automatic rollback. A failed call does not undo an earlier committed global.
        var ok = _manager.Execute(new CommandRequest
        {
            Action = "startup",
            Script = @"$global:Counter = 1"
        });
        Assert.True(ok.Success);

        var failed = _manager.Execute(new CommandRequest
        {
            Action = "update",
            Script = @"$global:Counter = 2; throw 'boom'"
        });
        Assert.False(failed.Success);

        // The mutation that ran before the throw is still in effect.
        var read = _manager.Execute(new CommandRequest
        {
            Action = "read",
            Script = @"[PSCustomObject]@{ counter = $global:Counter }"
        });
        Assert.True(read.Success);
        Assert.Equal("2", read.OutputData["counter"]?.ToString());
    }

    [Fact]
    public void Execute_ActionVariable_AvailableInsideCrudChildScope()
    {
        // $Action is injected and readable from inside the CRUD child scope.
        var response = _manager.Execute(new CommandRequest
        {
            Action = "update",
            Script = @"[PSCustomObject]@{ action_was = $Action }"
        });
        Assert.True(response.Success);
        Assert.Equal("update", response.OutputData["action_was"]?.ToString());
    }
}
