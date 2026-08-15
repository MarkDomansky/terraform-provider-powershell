# Data-exchange tests, driven by real terraform against the compiled provider.
# These prove the provider carries more than a flat 'id': rich, nested data
# (scalars of several types, arrays, nested objects, arrays of objects) survives
# the round-trip into terraform state, and one resource's output_data can be fed
# as another resource's input_data so values flow between resources.
#
# The terraform config lives in real .tf files under tf/resource-chaining/ so it
# can be parsed and validated by terraform tooling.

BeforeAll {
    Import-Module (Join-Path $PSScriptRoot 'PSTerraformProvider.psm1') -Force
    Write-TFLog 'Chaining suite: building provider + staging sidecar'
    Initialize-ProviderBin | Out-Null

    $script:ChainConfigPath = Join-Path $PSScriptRoot 'tf/resource-chaining'
}

Describe 'complex data exchange between resources (compiled provider via terraform)' {

    BeforeAll {
        Write-TFLog 'applying producer/consumer chaining config' 'STEP'
        $script:ws = New-TFWorkspace -ConfigPath $script:ChainConfigPath
        Invoke-TF -Workspace $script:ws -Arguments @('apply', '-auto-approve', '-no-color') | Out-Null
        $script:out = Get-TFOutput -Workspace $script:ws
    }

    AfterAll {
        if ($script:ws) { Remove-TFWorkspace -Workspace $script:ws }
    }

    Context 'producer returns rich nested data into state' {
        It 'round-trips scalars of several types' {
            $script:out.producer.value.id      | Should -Be 'producer'
            $script:out.producer.value.name    | Should -Be 'widget'
            [int]$script:out.producer.value.count | Should -Be 3
            $script:out.producer.value.enabled | Should -BeTrue
        }

        It 'round-trips an array of strings' {
            $script:out.producer.value.tags    | Should -HaveCount 3
            $script:out.producer.value.tags[0] | Should -Be 'alpha'
            $script:out.producer.value.tags[2] | Should -Be 'gamma'
        }

        It 'round-trips a nested object' {
            $script:out.producer.value.nested.host      | Should -Be 'example.com'
            [int]$script:out.producer.value.nested.port | Should -Be 8080
        }

        It 'round-trips an array of objects' {
            $script:out.producer.value.items        | Should -HaveCount 3
            $script:out.producer.value.items[0].key | Should -Be 'a'
            [int]$script:out.producer.value.items[2].value | Should -Be 3
        }
    }

    Context 'consumer receives the producer output as its input' {
        It 'reads scalar fields from the upstream resource' {
            $script:out.consumer.value.upstream_id | Should -Be 'producer'
            $script:out.consumer.value.name        | Should -Be 'widget'
            $script:out.consumer.value.enabled     | Should -BeTrue
        }

        It 'reaches into the upstream nested object' {
            # Built from $InputData.nested.host and $InputData.nested.port.
            $script:out.consumer.value.endpoint | Should -Be 'example.com:8080'
        }

        It 'reaches into the upstream arrays' {
            [int]$script:out.consumer.value.tag_count | Should -Be 3
            $script:out.consumer.value.first_tag      | Should -Be 'alpha'
            $script:out.consumer.value.first_key      | Should -Be 'a'
            # Sum of the upstream items' values: 1 + 2 + 3.
            [int]$script:out.consumer.value.sum       | Should -Be 6
        }

        It 'preserves deep structure across a second hop' {
            # The consumer echoed the upstream nested object and array of objects
            # straight back out, so the deep shape must survive producer ->
            # consumer -> state intact.
            [int]$script:out.consumer.value.nested.port | Should -Be 8080
            $script:out.consumer.value.items            | Should -HaveCount 3
            $script:out.consumer.value.items[1].key     | Should -Be 'b'
            [int]$script:out.consumer.value.items[1].value | Should -Be 2
        }
    }
}
