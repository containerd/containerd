# Prepare windows environment for building and running containerd tests

$PACKAGES= @{ mingw = "10.2.0"; git = ""; golang = "1.17.7"; make = ""; nssm = "" }

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

# Pull junit conversion tool
go install github.com/jstemmer/go-junit-report@v0.9.1

# Get critctl tool. Used for cri-integration tests
$CRICTL_DOWNLOAD_URL="https://github.com/kubernetes-sigs/cri-tools/releases/download/v1.21.0/crictl-v1.21.0-windows-amd64.tar.gz"
curl.exe -L $CRICTL_DOWNLOAD_URL -o c:\crictl.tar.gz
tar -xvf c:\crictl.tar.gz
mv crictl.exe "${userGoBin}\crictl.exe" # Move crictl somewhere in path
