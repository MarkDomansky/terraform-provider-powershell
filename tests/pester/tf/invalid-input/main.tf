terraform {
  required_providers {
    powershell = { source = "markdomansky/powershell" }
  }
}

# input_data is deliberately not valid JSON: the provider's plan-time validator
# must reject it with a pointed diagnostic instead of silently handing the
# scripts an empty $InputData.

provider "powershell" {}

resource "powershell_script" "bad" {
  create_script = "[PSCustomObject]@{ id = 'bad-1' }"
  read_script   = "[PSCustomObject]@{ id = 'bad-1' }"
  delete_script = "# noop"

  input_data = "{ this is not json"
}
