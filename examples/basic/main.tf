terraform {
  required_providers {
    powershell = {
      source = "registry.terraform.io/markdomansky/powershell"
    }
  }
}

# Basic example: manage a local file using PowerShell scripts
provider "powershell" {
  # Optional startup script to initialize provider-level state
  startup_script = <<-PS
    $global:ProviderState = @{}
    $global:ProviderState["initialized_at"] = (Get-Date).ToString("o")
    Write-Host "Provider initialized at $($global:ProviderState['initialized_at'])"
  PS
}

resource "powershell_script" "hello_file" {
  create_script = <<-PS
    $path = Join-Path $InputData.directory "$($InputData.filename)"
    New-Item -Path $path -ItemType File -Value $InputData.content -Force | Out-Null
    [PSCustomObject]@{
      id           = $InputData.filename
      full_path    = $path
      content      = $InputData.content
      created_at   = (Get-Date).ToString("o")
      provider_init = $global:ProviderState["initialized_at"]
    }
  PS

  read_script = <<-PS
    $path = Join-Path $InputData.directory "$($InputData.id)"
    if (Test-Path $path) {
      $content = Get-Content $path -Raw
      [PSCustomObject]@{
        id        = $InputData.id
        full_path = $path
        content   = $content
      }
    }
    # If file doesn't exist, nothing is emitted -> resource removed from state
  PS

  update_script = <<-PS
    $path = Join-Path $InputData.directory "$($InputData.id)"
    Set-Content -Path $path -Value $InputData.content -NoNewline
    [PSCustomObject]@{
      id        = $InputData.id
      full_path = $path
      content   = $InputData.content
      updated_at = (Get-Date).ToString("o")
    }
  PS

  delete_script = <<-PS
    $path = Join-Path $InputData.directory "$($InputData.id)"
    Remove-Item -Path $path -Force -ErrorAction SilentlyContinue
  PS

  input_data = jsonencode({
    directory = path.root
    filename  = "hello.txt"
    content   = "Hello from Terraform PowerShell Provider!"
  })
}

output "file_output" {
  value = jsondecode(powershell_script.hello_file.output_data)
}
