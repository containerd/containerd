# Prepare windows environment for building and running containerd tests

$PACKAGES= "mingw", "git", "golang", "make"

write-host "Downloading chocolatey package"
curl.exe -L "https://packages.chocolatey.org/chocolatey.0.10.15.nupkg" -o 'c:\choco.zip'
Expand-Archive "c:\choco.zip" -DestinationPath "c:\choco"

write-host "Installing choco"
& "c:\choco\tools\chocolateyInstall.ps1"

write-host "Set choco.exe path."
$env:PATH+=";C:\ProgramData\chocolatey\bin"

write-host "Install necessary packages"

foreach ($package in $PACKAGES) {
    choco.exe install $package --yes
}

write-host "Set up environment."

$path = ";c:\Program Files\Git\bin;c:\Program Files\Go\bin;c:\Users\azureuser\go\bin;c:\containerd\bin"
$env:PATH+=$path

write-host $env:PATH

[Environment]::SetEnvironmentVariable("PATH", $env:PATH, 'User')

# Prepare Log dir
mkdir c:\Logs

# Pull junit conversion tool
go get -u github.com/jstemmer/go-junit-report

# Get critctl tool. Used for cri-integration tests
$CRICTL_DOWNLOAD_URL="https://github.com/kubernetes-sigs/cri-tools/releases/download/v1.21.0/crictl-v1.21.0-windows-amd64.tar.gz"
curl.exe -L $CRICTL_DOWNLOAD_URL -o c:\crictl.tar.gz
tar -xvf c:\crictl.tar.gz
mv crictl.exe c:\Users\azureuser\go\bin\crictl.exe # Move crictl somewhere in path