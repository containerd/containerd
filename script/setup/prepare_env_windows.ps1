# Prepare windows environment for building and running containerd tests

# Disable Windows Defender real time monitoring. Real time monitoring consumes a lot of
# CPU and slows down tests as images are unarchived, and is not really needed in a short
# lived test environment.
Set-MpPreference -DisableRealtimeMonitoring:$true

$PACKAGES= @{ mingw = "10.2.0"; git = ""; golang = "1.19.6"; make = ""; nssm = "" }

Write-Host "Downloading chocolatey package"
curl.exe -L "https://packages.chocolatey.org/chocolatey.0.10.15.nupkg" -o 'c:\choco.zip'
Expand-Archive "c:\choco.zip" -DestinationPath "c:\choco"

Write-Host "Installing choco"
& "c:\choco\tools\chocolateyInstall.ps1"

Write-Host "Set choco.exe path."
$env:PATH+=";C:\ProgramData\chocolatey\bin"

Write-Host "Install necessary packages"

foreach ($package in $PACKAGES.Keys) {
    $command = "choco.exe install $package --yes"
    $version = $PACKAGES[$package]
    if (-Not [string]::IsNullOrEmpty($version)) {
        $command += " --version $version"
    }
    Invoke-Expression $command
}

Write-Host "Set up environment."

$userGoBin = "${env:HOME}\go\bin"
$path = ";c:\Program Files\Git\bin;c:\Program Files\Go\bin;${userGoBin};c:\containerd\bin"
$env:PATH+=$path

Write-Host $env:PATH

[Environment]::SetEnvironmentVariable("PATH", $env:PATH, 'User')

# Prepare Log dir
mkdir c:\Logs

# Log go env for future reference:
go env > c:\Logs\go-env.txt
cat c:\Logs\go-env.txt

# Pull junit conversion tool
go install github.com/jstemmer/go-junit-report@v0.9.1

# Get critctl tool. Used for cri-integration tests
$CRICTL_DOWNLOAD_URL="https://github.com/kubernetes-sigs/cri-tools/releases/download/v1.21.0/crictl-v1.21.0-windows-amd64.tar.gz"
curl.exe -L $CRICTL_DOWNLOAD_URL -o c:\crictl.tar.gz
tar -xvf c:\crictl.tar.gz
mv crictl.exe "${userGoBin}\crictl.exe" # Move crictl somewhere in path
