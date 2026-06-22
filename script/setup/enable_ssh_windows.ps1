Param(
    [parameter(Mandatory=$true)]
    [string]$SSHPublicKey
)

$ErrorActionPreference = "Stop"


function Set-SSHPublicKey {
    if(!$SSHPublicKey) {
        return
    }
    $authorizedKeysFile = Join-Path $env:ProgramData "ssh\administrators_authorized_keys"
    Set-Content -Path $authorizedKeysFile -Value $SSHPublicKey -Encoding ascii
    $acl = Get-Acl $authorizedKeysFile
    $acl.SetAccessRuleProtection($true, $false)
    $administratorsRule = New-Object system.security.accesscontrol.filesystemaccessrule("Administrators", "FullControl", "Allow")
    $systemRule = New-Object system.security.accesscontrol.filesystemaccessrule("SYSTEM", "FullControl", "Allow")
    $acl.SetAccessRule($administratorsRule)
    $acl.SetAccessRule($systemRule)
    $acl | Set-Acl
}

# Install OpenSSH
$(

Get-WindowsCapability -Online -Name OpenSSH* | Add-WindowsCapability -Online
Set-Service -Name sshd -StartupType Automatic
Start-Service sshd

# Authorize SSH key
Set-SSHPublicKey

# Set PowerShell as default shell
New-ItemProperty -Force -Path "HKLM:\SOFTWARE\OpenSSH" -PropertyType String `
                 -Name DefaultShell -Value "C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe"
) *>$1 >> c:\output.txt