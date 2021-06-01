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
$ make install.deps
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
$ docker pull k8s.gcr.io/pause:3.5
  3.5: Pulling from pause
  019d8da33d91: Pull complete
  Digest: sha256:1ff6c18fbef2045af6b9c16bf034cc421a29027b800e4f9b68ae9b1cb3e9ae07
  Status: Downloaded newer image for k8s.gcr.io/pause:3.5
  k8s.gcr.io/pause:3.5
$ docker save k8s.gcr.io/pause:3.5 -o pause.tar
```
Then use `ctr` to load the container image into the container runtime:
```console
# The cri plugin uses the "k8s.io" containerd namespace.
$ sudo ctr -n=k8s.io images import pause.tar
  Loaded image: k8s.gcr.io/pause:3.5
```
List images and inspect the pause image:
```console
$ sudo crictl images
IMAGE                       TAG                 IMAGE ID            SIZE
docker.io/library/busybox   latest              f6e427c148a76       728kB
k8s.gcr.io/pause            3.5                 ed210e3e4a5ba       683kB
$ sudo crictl inspecti ed210e3e4a5ba
  ... displays information about the pause image.
$ sudo crictl inspecti k8s.gcr.io/pause:3.5
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
RuntimeVersion:  1.0.0-beta.1-186-gdd47a72-TEST
RuntimeApiVersion:  v1alpha2
```
## Display Status & Configuration Information about Containerd & The CRI Plugin
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
  "config": {
    "containerd": {
      "snapshotter": "overlayfs",
      "runtime": "io.containerd.runtime.v1.linux"
    },
    "cni": {
      "binDir": "/opt/cni/bin",
      "confDir": "/etc/cni/net.d"
    },
    "registry": {
      "mirrors": {
        "docker.io": {
          "endpoint": [
            "https://registry-1.docker.io"
          ]
        }
      }
    },
    "streamServerPort": "10010",
    "sandboxImage": "k8s.gcr.io/pause:3.5",
    "statsCollectPeriod": 10,
    "containerdRootDir": "/var/lib/containerd",
    "containerdEndpoint": "unix:///run/containerd/containerd.sock",
    "rootDir": "/var/lib/containerd/io.containerd.grpc.v1.cri",
    "stateDir": "/run/containerd/io.containerd.grpc.v1.cri",
  },
  "golang": "go1.10"
}
```
## More Information
See [here](https://github.com/kubernetes-sigs/cri-tools/blob/master/docs/crictl.md)
for information about crictl.
