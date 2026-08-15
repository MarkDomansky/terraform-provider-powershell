# create.ps1 - remote-session probe. This script runs ON hop1 (the computer the
# provider opened its session to). It reports the hostname it actually landed on,
# and, when a second hop is configured via input_data, opens a further PSSession
# from hop1 to hop2 and reports that hostname too. GetHostName() is cross-platform
# so the same probe works for a Windows WinRM hop or a Linux SSH hop.
#
# The second hop supports two transports, selected by hop2_type:
#   winrm (default) - New-PSSession -ComputerName with a username/password credential
#                     (Negotiate). Used by the Windows double-hop scenario.
#   ssh             - New-PSSession -HostName with a key file. This is how a Linux hop1
#                     (e.g. hop0linux) reaches a Windows host, since PowerShell on Linux
#                     has no WinRM client. hop2_key_file is a path ON hop1.
$result = [ordered]@{
    id        = 'remote-probe'
    hop1      = [System.Net.Dns]::GetHostName()
    # OSVersion.Platform is 'Win32NT' on Windows and 'Unix' on Linux/macOS, so the
    # caller can prove which OS each hop actually landed on (e.g. a Linux runner
    # confirming it reached a Windows host).
    platform1 = [System.Environment]::OSVersion.Platform.ToString()
}

if ($InputData.hop2_host) {
    $hop2Type = if ($InputData.hop2_type) { "$($InputData.hop2_type)".ToLowerInvariant() } else { 'winrm' }
    if ($hop2Type -eq 'ssh') {
        # SSH second hop (Linux -> Windows): key-file auth, no interactive prompts.
        $sshArgs = @{
            HostName    = $InputData.hop2_host
            UserName    = $InputData.hop2_username
            ErrorAction = 'Stop'
            # accept-new so a first-time host key never blocks the non-interactive ssh.
            Options     = @{ StrictHostKeyChecking = 'accept-new' }
        }
        if ($InputData.hop2_key_file) { $sshArgs.KeyFilePath = $InputData.hop2_key_file }
        if ($InputData.hop2_port)     { $sshArgs.Port = [int]$InputData.hop2_port }
        $sess = New-PSSession @sshArgs
    } else {
        # WinRM second hop with explicit fresh credentials (avoids the double-hop
        # delegation problem).
        $sec  = ConvertTo-SecureString $InputData.hop2_password -AsPlainText -Force
        $cred = [System.Management.Automation.PSCredential]::new($InputData.hop2_username, $sec)
        $sess = New-PSSession -ComputerName $InputData.hop2_host -Credential $cred -Authentication Negotiate -ErrorAction Stop
    }
    try {
        $hop2 = Invoke-Command -Session $sess -ScriptBlock {
            [PSCustomObject]@{
                host     = [System.Net.Dns]::GetHostName()
                platform = [System.Environment]::OSVersion.Platform.ToString()
            }
        }
        $result.hop2      = $hop2.host
        $result.platform2 = $hop2.platform
    } finally {
        Remove-PSSession $sess
    }
}

[PSCustomObject]$result
