# Test image overview

Test images for Linux can be built as usual using buildx.

While it is possible to build Windows docker images on Linux (if we avoid the ```RUN``` or ```WORKDIR``` options), the ```volume-ownership``` and ```volume-copy-up``` images need to be built on Windows for the tests to be relevant. The reason for this is that when building images on Linux, Windows specific security info (DACL and ownership) does not get attached to the test files and folders inside the image. The ```TestVolumeCopyUp``` and ```TestVolumeOwnership``` tests will not be relevant, as owners of the files will always be ```ContainerAdministrator```.

Building images on Windows nodes also allows us to potentially add new users inside the images or enable new testing scenarios that require different services or applications to run inside the container.

This document describes the needed bits to build the Windows container images on a remote Windows node.

## Setting up the Windows build node

We can build images for all relevant Windows versions on a single Windows node as long as that Windows node is a version greater or equal to the image versions we're trying to build. For example, on a Windows Server 2022 node, we can build images for 1809, 2004, 20H2 and ltsc2022, while if we were running on Windows server 2019 machine, we would only be able to generate images for 1809. To build images for different versions of Windows, we need to enable the ```Hyper-V``` role, and use ```--isolation=hyperv``` as an argument to docker build.

Note, this will also work if nested hyperv is enabled. This means that the images can be built on Azure (nested Hyper-V is enabled by default), or on any modern linux machine using KVM and libvirt.
Note, at the time of this writing, the recommended version to build on is Windows Server 2022 (ltsc2022).


### Enabling nested VMX on Libvirt

To enable nested Hyper-V on libvirt, simply install Windows Server 2022 as usual, then shutdown the guest and edit it's config:

```bash
# replace win2k22 with the name of your Windows VM
virsh edit win2k22
```

and add/edit the CPU section to look like this:

```xml
<cpu mode='custom' match='exact' check='partial'>
    <model fallback='allow'>Broadwell</model>
    <feature policy='require' name='vmx'/>
</cpu>
```

Hyper-V should now work inside your KVM machine. It's not terribly fast, but it should suffice for building images.

### Enable necessary roles

Install the needed roles and tools:

```powershell
# Enable Hyper-V and management tools
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V,Microsoft-Hyper-V-Management-Clients,Microsoft-Hyper-V-Management-PowerShell -All -NoRestart

# Enable SSH (this can be skipped if you don't need it)
Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0

# Install Docker
Install-PackageProvider -Name NuGet -MinimumVersion 2.8.5.201 -Force -Confirm:$false
Install-Module -Name DockerMsftProvider -Repository PSGallery -Force -Confirm:$false
Install-Package -Name docker -ProviderName DockerMsftProvider -Force -Confirm:$false
```

At this point we can reboot for the changes to take effect:

```powershell
Restart-Computer -Force
```

### Configure needed services

Start sshd and enable it to run on startup:

```powershell
Start-Service sshd
Set-Service -Name sshd -StartupType 'Automatic'
```

Open Firewall port for ssh:

```powershell
New-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -DisplayName 'OpenSSH Server (sshd)' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22
```

These following steps are taken from the [k8s windows image builder helper page](https://github.com/kubernetes/kubernetes/blob/master/test/images/windows/README.md).


Enable TLS authentication for docker and enable remote access:

```powershell
# Replace YOUR_SERVER_IP_GOES_HERE with the IP addresses you'll use to access
# this node. This will be the private IP and VIP/Floating IP of the server.
docker run --isolation=hyperv --user=ContainerAdministrator --rm `
   -e SERVER_NAME=$(hostname) `
   -e IP_ADDRESSES=127.0.0.1,YOUR_SERVER_IP_GOES_HERE `
   -v "c:\programdata\docker:c:\programdata\docker" `
   -v "$env:USERPROFILE\.docker:c:\users\containeradministrator\.docker" stefanscherer/dockertls-windows:2.5.5
```

Restart Docker:

```powershell
Stop-Service docker
Start-Service docker
```

After this, the files (```ca.pem```, ```cert.pem``` and ```key.pem```) needed to authenticate to docker will be present in ```$env:USERPROFILE\.docker``` on the Windows machine. You will need to copy those files to your linux machine in ```$HOME/.docker```. They are needed in order to authenticate against the Windows docker daemon during our image build process.

Open Firewall port for docker:

```powershell
New-NetFirewallRule -Name 'Docker-TLS-In-TCP' -DisplayName 'Docker (TLS)' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 2376
```

Note, if you're running in a cloud, make sure you also open the port in your NSG/Security group.

## Building the images

With the above mentioned files copied to ```$HOME/.docker``` we can now start building the images:

```bash
git clone https://github.com/containerd/containerd
cd containerd/integration/images/volume-copy-up

make setup-buildx
make configure-docker
# 192.168.122.107 corresponds to the IP address of your windows build node.
# This builds the images and pushes them to the registry specified by PROJ
# The Windows images will be built on the Windows node and pushed from there.
# You will need to make sure that docker is configured and able to push to the
# project you want to push to.
make build-registry PROJ=docker.example.com REMOTE_DOCKER_URL=192.168.122.107:2376
# Create a manifest and update it with all supported operating systems and architectures.
make push-manifest PROJ=docker.samfira.com REMOTE_DOCKER_URL=192.168.122.107:2376
```
