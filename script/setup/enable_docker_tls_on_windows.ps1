Param(
    [parameter(Mandatory=$true)]
    [string[]]$IPAddresses
)

$ErrorActionPreference = "Stop"
$IPAddresses += "127.0.0.1"
$IPParams = $IPAddresses -join ","
mkdir $env:USERPROFILE\.docker

docker run --isolation=hyperv --user=ContainerAdministrator --rm `
   -e SERVER_NAME=$(hostname) `
   -e IP_ADDRESSES=$IPParams `
   -v "c:\programdata\docker:c:\programdata\docker" `
   -v "$env:USERPROFILE\.docker:c:\users\containeradministrator\.docker" stefanscherer/dockertls-windows:2.5.5

if ($LASTEXITCODE) {
    Throw "Failed to setup Docker TLS: $LASTEXITCODE"
}

Stop-Service docker
Start-Service docker
