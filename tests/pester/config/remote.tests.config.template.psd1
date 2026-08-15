# Remote-integration test configuration TEMPLATE.
#
# These scenarios drive the parameter-based remote suites (RemoteSession.Tests.ps1
# and Dns.Tests.ps1), which need REAL hosts and credentials and therefore cannot run
# in CI. To enable them on a machine that can reach the hosts:
#
#   1. Copy this file to  remote.tests.config.psd1   (same folder; it is gitignored).
#   2. Fill in real hosts/usernames, set Enabled = $true on the scenarios you want,
#      and replace every 'TBD' password (and any key file) with real values.
#
# Skip rules: a scenario runs only when Enabled = $true AND its SessionPassword
# (and Hop2Password, if present) is neither empty nor 'TBD'. SSH scenarios may use a
# key file instead of a password.
#
# Each scenario is a hashtable. Recognised keys:
#   Enabled                 [bool]   run this scenario?
#   SessionType             winrm | ssh | vmguest
#   SessionHost                      hop1 computer name / hostname (winrm, ssh)
#   SessionPort             [int]    optional port override
#   SessionUsername / SessionPassword
#   SessionUseSSL           [bool]   winrm over HTTPS
#   SessionAuthentication            winrm auth (Default/Negotiate/Kerberos/...)
#   SessionCertThumbprint            winrm client-cert auth
#   SessionConfigurationName         winrm endpoint, e.g. "PowerShell.7"
#   SessionKeyFile                   ssh private key path
#   SessionVmName / SessionVmId      vmguest (PowerShell Direct) target
#   Hop2Host / Hop2Username / Hop2Password   optional SECOND hop (done inside the
#                                    probe script with fresh creds, avoiding the
#                                    WinRM double-hop delegation problem)
#   ExpectedHostname                 substring the final reached hostname must match
#
# The 'Dns' scenario additionally uses: DnsServer (default "localhost", relative to
# SessionHost), Zone, RecordName, Ipv4Address, Ipv4AddressUpdated.

@{
    # --- hop0: a Linux VM over SSH (user/key/credential to be provided) ----------
    Hop0Linux = @{
        Enabled          = $false
        SessionType      = 'ssh'
        SessionHost      = ''              # e.g. linux01.domain1.local
        SessionUsername  = ''
        SessionKeyFile   = ''              # path to private key, or use SessionPassword
        SessionPassword  = 'TBD'
        ExpectedHostname = ''              # short hostname the probe should report
    }

    # --- single hop: WinRM to webdebug -------------------------------------------
    Hop1WinRM = @{
        Enabled               = $false
        SessionType           = 'winrm'
        SessionHost           = 'webdebug.domain1.local'
        SessionUsername       = 'markd@domain1.local'
        SessionPassword       = 'TBD'
        SessionAuthentication = 'Negotiate'
        SessionUseSSL         = $false
        ExpectedHostname      = 'webdebug'
    }

    # --- double hop: WinRM to webdebug (hop1), then onward to dc25a (hop2) --------
    DoubleHopWinRM = @{
        Enabled               = $false
        SessionType           = 'winrm'
        SessionHost           = 'webdebug.domain1.local'
        SessionUsername       = 'markd@domain1.local'
        SessionPassword       = 'TBD'
        SessionAuthentication = 'Negotiate'
        Hop2Host              = 'dc25a.domain1.local'
        Hop2Username          = 'markd@domain1.local'
        Hop2Password          = 'TBD'
        ExpectedHostname      = 'dc25a'
    }

    # --- Linux -> Windows over SSH (used by LinuxToWindows.Tests.ps1) -------------
    # Runs only on a Linux host. The Windows target must run OpenSSH with the
    # PowerShell subsystem. SSH remoting cannot take a password non-interactively, so
    # set SessionKeyFile to a private key (the public key must be authorized on the
    # Windows host for SessionUsername). Username is typically 'user@domain' or
    # 'domain\user' for a domain account, or a local account name.
    LinuxToWindowsSSH = @{
        Enabled          = $false
        SessionType      = 'ssh'
        SessionHost      = 'webdebug.domain1.local'
        SessionUsername  = 'markd@domain1.local'
        SessionKeyFile   = ''                # REQUIRED: path to the SSH private key
        SessionPassword  = ''                # unsupported for SSH; leave empty
        SessionPort      = ''                # optional override (default 22)
        ExpectedHostname = 'webdebug'
        ExpectedPlatform = 'Win32NT'         # proves the Linux client reached Windows
    }

    # --- vmguest: PowerShell Direct into a Hyper-V VM -----------------------------
    VmGuest = @{
        Enabled          = $false
        SessionType      = 'vmguest'
        SessionVmName    = ''              # the Hyper-V VM name
        SessionUsername  = ''
        SessionPassword  = 'TBD'
        ExpectedHostname = ''
    }

    # --- DNS integration target (a Windows DNS server, e.g. the DC) ---------------
    Dns = @{
        Enabled               = $false
        SessionType           = 'winrm'
        SessionHost           = 'dc25a.domain1.local'
        SessionUsername       = 'markd@domain1.local'
        SessionPassword       = 'TBD'
        SessionAuthentication = 'Negotiate'
        DnsServer             = 'localhost'  # relative to SessionHost (the DC itself)
        Zone                  = 'domain1.local'
        RecordName            = 'pstf-itest'
        Ipv4Address           = '10.0.0.50'
        Ipv4AddressUpdated    = '10.0.0.51'
    }
}
