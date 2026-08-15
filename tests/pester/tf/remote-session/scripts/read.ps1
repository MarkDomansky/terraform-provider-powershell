# read.ps1 - the probe is a pure connectivity check with nothing to read back; just
# re-assert its stable id so Terraform keeps it in state.
[PSCustomObject]@{ id = 'remote-probe' }
