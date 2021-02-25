# LinuxKit Kubernetes project

The [LinuxKit](https://github.com/linuxkit/kubernetes) is a project for building os images for kubernetes master and worker node virtual machines.

When the images are built with cri-containerd as the `KUBE_RUNTIME` option they will use `cri-containerd` as their execution backend.
```
make all KUBE_RUNTIME=cri-containerd
```
