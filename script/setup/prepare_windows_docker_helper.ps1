$ErrorActionPreference = "Stop"

# Enable Hyper-V and management tools
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V,Microsoft-Hyper-V-Management-Clients,Microsoft-Hyper-V-Management-PowerShell -All -NoRestart

# Enable SSH (this can be skipped if you don't need it)
Add-WindowsCapability -Online -Name OpenSSH*

# Install Docker
Install-PackageProvider -Name NuGet -MinimumVersion 2.8.5.201 -Force -Confirm:$false
Install-Module -Name DockerMsftProvider -Repository PSGallery -Force -Confirm:$false
Install-Package -Name docker -ProviderName DockerMsftProvider -Force -Confirm:$false

# Open SSH port
New-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -DisplayName 'OpenSSH Server (sshd)' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22

# Open Docker port
New-NetFirewallRule -Name 'Docker-TLS-In-TCP' -DisplayName 'Docker (TLS)' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 2376

# Restart
Restart-Computer -Force
