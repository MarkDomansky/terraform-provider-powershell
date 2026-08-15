terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

provider "powershell" {}

# Producer returns rich, nested output: scalars of several types, an array of
# strings, a nested object, and an array of objects. This proves more than an
# 'id' survives the round-trip back into terraform state.
resource "powershell_script" "producer" {
  create_script = <<-PS
    [PSCustomObject]@{
      id      = "producer"
      name    = "widget"
      count   = 3
      enabled = $true
      tags    = @("alpha", "beta", "gamma")
      nested  = @{
        host = "example.com"
        port = 8080
      }
      items = @(
        @{ key = "a"; value = 1 },
        @{ key = "b"; value = 2 },
        @{ key = "c"; value = 3 }
      )
    }
  PS
  read_script   = <<-PS
    [PSCustomObject]@{ id = "producer" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "producer" }
  PS
  delete_script = <<-PS
    # noop
  PS
}

# Consumer takes the producer's entire output as its input. terraform sequences
# the two because input_data references the producer's computed output_data.
# The consumer reaches into the nested structure (object fields, array length,
# array-of-object elements) and echoes the deep parts straight back out, proving
# complex data flows between resources, not just a flat id.
resource "powershell_script" "consumer" {
  input_data = powershell_script.producer.output_data

  create_script = <<-PS
    $sum = 0
    foreach ($i in $InputData.items) { $sum += [int]$i.value }
    [PSCustomObject]@{
      id          = "consumer"
      upstream_id = $InputData.id
      name        = $InputData.name
      endpoint    = "$($InputData.nested.host):$($InputData.nested.port)"
      tag_count   = $InputData.tags.Count
      first_tag   = $InputData.tags[0]
      first_key   = $InputData.items[0].key
      sum         = $sum
      enabled     = $InputData.enabled
      # Echo the nested object and array of objects back out to prove the deep
      # structure survives a second hop (producer -> consumer -> state).
      nested      = $InputData.nested
      items       = $InputData.items
    }
  PS
  read_script   = <<-PS
    [PSCustomObject]@{ id = "consumer" }
  PS
  update_script = <<-PS
    [PSCustomObject]@{ id = "consumer" }
  PS
  delete_script = <<-PS
    # noop
  PS
}

output "producer" { value = jsondecode(powershell_script.producer.output_data) }
output "consumer" { value = jsondecode(powershell_script.consumer.output_data) }
