CRICTL User Guide
=================
This document presumes you already have `containerd` and `cri-containerd`
installed and running.

This document is for developers who wish to debug, inspect, and manage their pods,
containers, and container images.

Before generating issues against this document, `containerd`, `cri-containerd`,
or `crictl` please make sure the issue has not already been submitted.

## Install crictl
If you have not already installed crictl please install the version compatible
with the `cri-containerd` you are using. If you are a user, your deployment
should have installed crictl for you. If not, get it from your release tarball.
If you are a developer the current version of crictl is specified [here](../hack/utils.sh).
A helper command has been included to install the dependencies at the right version:
```console
$ make install.deps
```
* Note: The file named `/etc/crictl.yaml` is used to configure crictl
so you don't have to repeatedly specify the runtime sock used to connect crictl
to the container runtime:
```console
$ cat /etc/crictl.yaml
runtime-endpoint: /var/run/cri-containerd.sock
image-endpoint: /var/run/cri-containerd.sock
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

## Directly Load a Container Image
Another way to load an image into the container runtime is with the load
command. With the load command you inject a container image into the container
runtime from a file. First you need to create a container image tarball. For
example to create an image tarball for a pause container using Docker:
```console
$ docker pull k8s.gcr.io/pause-amd64:3.1
  3.1: Pulling from pause-amd64
  67ddbfb20a22: Pull complete
  Digest: sha256:59eec8837a4d942cc19a52b8c09ea75121acc38114a2c68b98983ce9356b8610
  Status: Downloaded newer image for k8s.gcr.io/pause-amd64:3.1
$ docker save k8s.gcr.io/pause-amd64:3.1 -o pause.tar
```
Then load the container image into the container runtime:
```console
$ sudo ctrcri load pause.tar
  Loaded image: k8s.gcr.io/pause-amd64:3.1
```
List images and inspect the pause image:
```console
$ sudo crictl images
IMAGE                       TAG                 IMAGE ID            SIZE
docker.io/library/busybox   latest              f6e427c148a76       728kB
k8s.gcr.io/pause-amd64      3.1                 da86e6ba6ca19       746kB
$ sudo crictl inspecti da86e6ba6ca19
  ... displays information about the pause image.
$ sudo crictl inspecti k8s.gcr.io/pause-amd64:3.1
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

## Create and Run a Container in a Pod Sandbox (using a config file)
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
$ crictl ps
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
CONTAINER           CPU %               MEM                 DISK                INODES
0a2c761303163f      0.00                991.2kB             16.38kB             6
```
* Other commands to manage the container include `stop ID` to stop a running
container and `rm ID` to remove a container.
## Display Version Information
```console
$ crictl version
Version:  0.1.0
RuntimeName:  cri-containerd
RuntimeVersion:  1.0.0-alpha.1-167-g737efe7-dirty
RuntimeApiVersion:  0.0.0
```
## Display Status & Configuration Information about Containerd & CRI-Containerd
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
      "rootDir": "/var/lib/containerd",
      "snapshotter": "overlayfs",
      "endpoint": "/run/containerd/containerd.sock",
      "runtime": "io.containerd.runtime.v1.linux"
    },
    "cni": {
      "binDir": "/opt/cni/bin",
      "confDir": "/etc/cni/net.d"
    },
    "socketPath": "/var/run/cri-containerd.sock",
    "rootDir": "/var/lib/cri-containerd",
    "streamServerPort": "10010",
    "sandboxImage": "gcr.io/google_containers/pause:3.0",
    "statsCollectPeriod": 10,
    "oomScore": -999,
    "enableProfiling": true,
    "profilingPort": "10011",
    "profilingAddress": "127.0.0.1"
  }
}
```
## More Information
See [here](https://github.com/kubernetes-incubator/cri-tools/blob/master/docs/crictl.md)
for information about crictl.
