CRICTL User Guide
=================
This document presumes you already have `containerd` with the `cri` plugin installed and running.

This document is for developers who wish to debug, inspect, and manage their pods,
containers, and container images.

Before generating issues against this document, `containerd`, `containerd/cri`,
or `crictl` please make sure the issue has not already been submitted.

## Install crictl
If you have not already installed crictl please install the version compatible
with the `cri` plugin you are using. If you are a user, your deployment
should have installed crictl for you. If not, get it from your release tarball.
If you are a developer the current version of crictl is specified [here](/script/setup/critools-version).
A helper command has been included to install the dependencies at the right version:
```console
$ make install-deps
```
* Note: The file named `/etc/crictl.yaml` is used to configure crictl
so you don't have to repeatedly specify the runtime sock used to connect crictl
to the container runtime:
```console
$ cat /etc/crictl.yaml
runtime-endpoint: unix:///run/containerd/containerd.sock
image-endpoint: unix:///run/containerd/containerd.sock
timeout: 10
debug: true
```

## Download and Inspect a Container Image
The pull command tells the container runtime to download a container image from
a container registry.
```console
$ crictl pull busybox
  ...
$ crictl inspecti busybox
  ... displays information about the image.
```

***Note:*** If you get an error similar to the following when running a `crictl`
command (and your containerd instance is already running):
```console
crictl info
FATA[0000] getting status of runtime failed: rpc error: code = Unimplemented desc = unknown service runtime.v1alpha2.RuntimeService
```
This could be that you are using an incorrect containerd configuration (maybe
from a Docker install). You will need to update your containerd configuration
to the containerd instance that you are running. One way of doing this is as
follows:
```console
$ mv /etc/containerd/config.toml /etc/containerd/config.bak
$ containerd config default > /etc/containerd/config.toml
```

## Directly Load a Container Image
Another way to load an image into the container runtime is with the load
command. With the load command you inject a container image into the container
runtime from a file. First you need to create a container image tarball. For
example to create an image tarball for a pause container using Docker:
```console
$ docker pull registry.k8s.io/pause:3.10.1
  3.10.1: Pulling from pause
  19e4906e80f6: Pull complete
  Digest: sha256:278fb9dbcca9518083ad1e11276933a2e96f23de604a3a08cc3c80002767d24c
  Status: Downloaded newer image for registry.k8s.io/pause:3.10.1
  registry.k8s.io/pause:3.10.1
$ docker save registry.k8s.io/pause:3.10.1 -o pause.tar
```
Then use `ctr` to load the container image into the container runtime:
```console
# The cri plugin uses the "k8s.io" containerd namespace.
$ sudo ctr -n=k8s.io images import pause.tar
  Loaded image: registry.k8s.io/pause:3.10.1
```
List images and inspect the pause image:
```console
$ sudo crictl images
IMAGE                       TAG                 IMAGE ID            SIZE
docker.io/library/busybox   latest              f6e427c148a76       728kB
registry.k8s.io/pause       3.10.1              cd073f4c5f6a8       320kB
$ sudo crictl inspecti cd073f4c5f6a8
  ... displays information about the pause image.
$ sudo crictl inspecti registry.k8s.io/pause:3.10.1
  ... displays information about the pause image.
```

## Run a pod sandbox (using a config file)
```console
$ cat sandbox-config.json
{
    "metadata": {
        "name": "nginx-sandbox",
        "namespace": "default",
        "attempt": 1,
        "uid": "hdishd83djaidwnduwk28bcsb"
    },
    "linux": {
    }
}

$ crictl runp sandbox-config.json
e1c83b0b8d481d4af8ba98d5f7812577fc175a37b10dc824335951f52addbb4e
$ crictl pods
PODSANDBOX ID       CREATED             STATE               NAME               NAMESPACE          ATTEMPT
e1c83b0b8d481       2 hours ago         SANDBOX_READY       nginx-sandbox      default            1
$ crictl inspectp e1c8
  ... displays information about the pod and the pod sandbox pause container.
```
* Note: As shown above, you may use truncated IDs if they are unique.
* Other commands to manage the pod include `stops ID` to stop a running pod and
`rmp ID` to remove a pod sandbox.

## Create and Run a Container in the Pod Sandbox (using a config file)
```console
$ cat container-config.json
{
  "metadata": {
      "name": "busybox"
  },
  "image":{
      "image": "busybox"
  },
  "command": [
      "top"
  ],
  "linux": {
  }
}

$ crictl create e1c83 container-config.json sandbox-config.json
0a2c761303163f2acaaeaee07d2ba143ee4cea7e3bde3d32190e2a36525c8a05
$ crictl ps -a
CONTAINER ID        IMAGE               CREATED             STATE               NAME                ATTEMPT
0a2c761303163       docker.io/busybox   2 hours ago         CONTAINER_CREATED   busybox             0
$ crictl start 0a2c
0a2c761303163f2acaaeaee07d2ba143ee4cea7e3bde3d32190e2a36525c8a05
$ crictl ps
CONTAINER ID        IMAGE               CREATED             STATE               NAME                ATTEMPT
0a2c761303163       docker.io/busybox   2 hours ago         CONTAINER_RUNNING   busybox             0
$ crictl inspect 0a2c7
  ... show detailed information about the container
```
## Exec a Command in the Container
```console
$ crictl exec -i -t 0a2c ls
bin   dev   etc   home  proc  root  sys   tmp   usr   var
```
## Display Stats for the Container
```console
$ crictl stats
CONTAINER           CPU %               MEM                 DISK              INODES
0a2c761303163f      0.00                983kB             16.38kB             6
```
* Other commands to manage the container include `stop ID` to stop a running
container and `rm ID` to remove a container.
## Display Version Information
```console
$ crictl version
Version:  0.1.0
RuntimeName:  containerd
RuntimeVersion:  v1.7.0
RuntimeApiVersion:  v1
```
## Display Status & Configuration Information about Containerd & The CRI Plugin
<details>
<p>

```console
$ crictl info
{
  "status": {
    "conditions": [
      {
        "type": "RuntimeReady",
        "status": true,
        "reason": "",
        "message": ""
      },
      {
        "type": "NetworkReady",
        "status": true,
        "reason": "",
        "message": ""
      }
    ]
  },
  "cniconfig": {
    "PluginDirs": [
      "/opt/cni/bin"
    ],
    "PluginConfDir": "/etc/cni/net.d",
    "PluginMaxConfNum": 1,
    "Prefix": "eth",
    "Networks": []
  },
  "config": {
    "containerd": {
      "snapshotter": "overlayfs",
      "defaultRuntimeName": "runc",
      "defaultRuntime": {
        "runtimeType": "",
        "runtimePath": "",
        "runtimeEngine": "",
        "PodAnnotations": [],
        "ContainerAnnotations": [],
        "runtimeRoot": "",
        "options": {},
        "privileged_without_host_devices": false,
        "privileged_without_host_devices_all_devices_allowed": false,
        "baseRuntimeSpec": "",
        "cniConfDir": "",
        "cniMaxConfNum": 0,
        "snapshotter": "",
        "sandboxMode": ""
      },
      "untrustedWorkloadRuntime": {
        "runtimeType": "",
        "runtimePath": "",
        "runtimeEngine": "",
        "PodAnnotations": [],
        "ContainerAnnotations": [],
        "runtimeRoot": "",
        "options": {},
        "privileged_without_host_devices": false,
        "privileged_without_host_devices_all_devices_allowed": false,
        "baseRuntimeSpec": "",
        "cniConfDir": "",
        "cniMaxConfNum": 0,
        "snapshotter": "",
        "sandboxMode": ""
      },
      "runtimes": {
        "runc": {
          "runtimeType": "io.containerd.runc.v2",
          "runtimePath": "",
          "runtimeEngine": "",
          "PodAnnotations": [],
          "ContainerAnnotations": [],
          "runtimeRoot": "",
          "options": {
            "BinaryName": "",
            "CriuImagePath": "",
            "CriuPath": "",
            "CriuWorkPath": "",
            "IoGid": 0,
            "IoUid": 0,
            "NoNewKeyring": false,
            "NoPivotRoot": false,
            "Root": "",
            "ShimCgroup": "",
            "SystemdCgroup": false
          },
          "privileged_without_host_devices": false,
          "privileged_without_host_devices_all_devices_allowed": false,
          "baseRuntimeSpec": "",
          "cniConfDir": "",
          "cniMaxConfNum": 0,
          "snapshotter": "",
          "sandboxMode": "podsandbox"
        }
      },
      "noPivot": false,
      "disableSnapshotAnnotations": true,
      "discardUnpackedLayers": false,
      "ignoreBlockIONotEnabledErrors": false,
      "ignoreRdtNotEnabledErrors": false
    },
    "cni": {
      "binDir": "/opt/cni/bin",
      "confDir": "/etc/cni/net.d",
      "maxConfNum": 1,
      "setupSerially": false,
      "confTemplate": "",
      "ipPref": ""
    },
    "registry": {
      "configPath": "",
      "mirrors": {},
      "configs": {},
      "auths": {},
      "headers": {}
    },
    "imageDecryption": {
      "keyModel": "node"
    },
    "disableTCPService": true,
    "streamServerAddress": "127.0.0.1",
    "streamServerPort": "0",
    "streamIdleTimeout": "4h0m0s",
    "enableSelinux": false,
    "selinuxCategoryRange": 1024,
    "sandboxImage": "registry.k8s.io/pause:3.10.1",
    "statsCollectPeriod": 10,
    "systemdCgroup": false,
    "enableTLSStreaming": false,
    "x509KeyPairStreaming": {
      "tlsCertFile": "",
      "tlsKeyFile": ""
    },
    "maxContainerLogSize": 16384,
    "disableCgroup": false,
    "disableApparmor": false,
    "restrictOOMScoreAdj": false,
    "maxConcurrentDownloads": 3,
    "disableProcMount": false,
    "unsetSeccompProfile": "",
    "tolerateMissingHugetlbController": true,
    "disableHugetlbController": true,
    "device_ownership_from_security_context": false,
    "ignoreImageDefinedVolumes": false,
    "netnsMountsUnderStateDir": false,
    "enableUnprivilegedPorts": false,
    "enableUnprivilegedICMP": false,
    "enableCDI": false,
    "cdiSpecDirs": [
      "/etc/cdi",
      "/var/run/cdi"
    ],
    "imagePullProgressTimeout": "1m0s",
    "drainExecSyncIOTimeout": "0s",
    "containerdRootDir": "/var/lib/containerd",
    "containerdEndpoint": "/run/containerd/containerd.sock",
    "rootDir": "/var/lib/containerd/io.containerd.grpc.v1.cri",
    "stateDir": "/run/containerd/io.containerd.grpc.v1.cri"
  },
  "golang": "go1.20.3",
  "lastCNILoadStatus": "OK",
  "lastCNILoadStatus.default": "OK"
}
```

</p>
</details>

## More Information
See [here](https://github.com/kubernetes-sigs/cri-tools/blob/master/docs/crictl.md)
for information about crictl.
