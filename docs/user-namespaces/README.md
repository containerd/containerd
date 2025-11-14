# Support for user namespaces

Kubernetes supports running pods with user namespace since v1.25. This document explains the
containerd support for this feature.

## What are user namespaces?

A user namespace isolates the user running inside the container from the one in the host.

A process running as root in a container can run as a different (non-root) user in the host; in
other words, the process has full privileges for operations inside the user namespace, but is
unprivileged for operations outside the namespace.

You can use this feature to reduce the damage a compromised container can do to the host or other
pods in the same node. There are several security vulnerabilities rated either HIGH or CRITICAL that
were not exploitable when user namespaces is active. It is expected user namespace will mitigate
some future vulnerabilities too.

See [the kubernetes documentation][kube-intro] for a high-level introduction to
user namespaces.

[kube-intro]: https://kubernetes.io/docs/concepts/workloads/pods/user-namespaces/#introduction

## Stack requirements

The Kubernetes implementation was redesigned in 1.27, so the requirements are different for versions
pre and post Kubernetes 1.27.

Please note that if you try to use user namespaces with containerd 1.6 or older, the `hostUsers:
false` setting in your pod.spec will be **silently ignored**.

### Kubernetes 1.25 and 1.26

 * Containerd 1.7
 * You can use runc or crun as the OCI runtime:
   * runc 1.1 or greater
   * crun 1.4.3 or greater

You can also use containerd 2.0 or above, but the same [requirements as Kubernetes 1.27 and
greater](#Kubernetes-127-and-greater) apply, except for the Linux kernel. Bear in mind that all the
requirements there apply, including file-systems supporting idmap mounts. You can use Linux
versions:

 * Linux 5.15: you will suffer from [the containerd 1.7 storage and latency
   limitations](#Limitations), as it doesn't support idmap mounts for overlayfs.
 * Linux 5.19 or greater (recommended): it doesn't suffer from any of the containerd 1.7
   limitations, as overlayfs started supporting idmap mounts on this kernel version.

### Kubernetes 1.27 and greater

 * Linux 6.3 or greater
 * Containerd 2.0 or greater
 * You can use runc or crun as the OCI runtime:
   * runc 1.2 or greater
   * crun 1.9 or greater

Furthermore, all the file-systems used by the volumes in the pod need kernel-support for idmap
mounts. Some popular file-systems that support idmap mounts in Linux 6.3 are: `btrfs`, `ext4`, `xfs`,
`fat`, `tmpfs`, `overlayfs`.

The kubelet is in charge of populating some files to the containers (like configmap, secrets, etc.).
The file-system used in that path needs to support idmap mounts too. See [the Kubernetes
documentation][kube-req] for more info on that.


[kube-req]: https://kubernetes.io/docs/concepts/workloads/pods/user-namespaces/#before-you-begin

## Creating a Kubernetes pod with user namespaces

First check your containerd, Linux and Kubernetes versions. If those are okay, then there is no
special configuration needed on conntainerd. You can just follow the steps in the [Kubernetes
website][kube-example].

[kube-example]: https://kubernetes.io/docs/tasks/configure-pod-container/user-namespaces/

# Limitations

You can check the limitations Kubernetes has [here][kube-limitations]. Note that different
Kubernetes versions have different limitations, be sure to check the site for the Kubernetes version
you are using.

Different containerd versions have different limitations too, those are highlighted in this section.

[kube-limitations]: https://kubernetes.io/docs/concepts/workloads/pods/user-namespaces/#limitations

### containerd 1.7

One limitation present in containerd 1.7 is that it needs to change the ownership of every file and
directory inside the container image, during Pod startup. This means it has a storage overhead, as
**the size of the container image is duplicated each time a pod is created**, and can significantly
impact the container startup latency, as doing such a copy takes time too.

You can mitigate this limitation by switching `/sys/module/overlay/parameters/metacopy` to `Y`. This
will significantly reduce the storage and performance overhead, as only the inode for each file of
the container image will be duplicated, but not the content of the file. This means it will use less
storage and it will be faster. However, it is not a panacea.

If you change the metacopy param, make sure to do it in a way that is persistent across reboots. You
should also be aware that this setting will be used for all containers, not just containers with
user namespaces enabled. This will affect all the snapshots that you take manually (if you happen to
do that). In that case, make sure to use the same value of `/sys/module/overlay/parameters/metacopy`
when creating and restoring the snapshot.

### containerd 2.0 and above

The storage and latency limitation from containerd 1.7 are not present in container 2.0 and above,
if you use the overlay snapshotter (this is used by default). It will not use more storage at all,
and there is no startup latency.

This is achieved by using the kernel feature idmap mounts with the container rootfs (the container
image). This allows an overlay file-system to expose the image with different UID/GID without copying
the files nor the inodes, just using a bind-mount.

Containerd by default will refuse to create a container with user namespaces, if overlayfs is the
snapshotter and the kernel running doesn't support idmap mounts for overlayfs.  This is to make sure
before falling back to the expensive chown (in terms of storage and pod startup latency), you
understand the implications and decide to opt-in. Please read the containerd 1.7 limitations for an
explanation of those.

If your kernel doesn't support idmap mounts for the overlayfs snapshotter, you will see an error
like:

```
failed to create containerd container: snapshotter "overlayfs" doesn't support idmap mounts on this host, configure `slow_chown` to allow a slower and expensive fallback
```

Linux supports idmap mounts on an overlayfs since version 5.19.

You can opt-in for the slow chown by adding the `slow_chown` field to your config in the overlayfs
snapshotter section, like this:

```
  [plugins."io.containerd.snapshotter.v1.overlayfs"]
    slow_chown = true
```

Note that only overlayfs users need to opt-in for the slow chown, as it as it is the only one that
containerd provides a better option (only the overlayfs snapshotter supports idmap mounts in
containerd). If you use another snapshotter, you will fall-back to the expensive chown without the
need to opt-in.

That being said, you can double check if your container is using idmap mounts for the container
image if you create a pod with user namespaces, exec into it and run:

```
mount | grep overlay
```

You should see a reference to the idmap mount in the `lowerdir` parameter, in this case we can see
`idmapped` used there:

```
overlay on / type overlay (rw,relatime,lowerdir=/tmp/ovl-idmapped823885363/0,upperdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/1018/fs,workdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/1018/work)
```

## Creating a container with user namespaces with `ctr`

You can also create a container with user namespaces using `ctr`. This is more low-level, be warned.

Create a directory where we will work:

```sh
mkdir -p /tmp/userns-test
cd /tmp/userns-test
```

Please note that we will need +x permissions to all components in the path to the rootfs (like
`/tmp` and `/tmp/rootfs`). So, it's recommended to do this inside `/tmp`, as that will have the
right permissions.

Create an OCI bundle:
```sh
# create the rootfs directory
mkdir rootfs

# export busybox via Docker into the rootfs directory
docker export $(docker create busybox) | tar -C rootfs -xvf -

# adjust the permissions
sudo chown -R 65536:65536 rootfs/
```

Copy [this config.json](./config.json) to `/tmp/userns-test`. Please note the process.root.path
field in the config.json it's pointing to the rootfs we just created. This **needs to be an
absolute path**.

Then create and start the container with:

```
sudo ctr c create --config config.json userns-test
sudo ctr t start userns-test
```

This will open a shell inside the container. You can run this, to verify you are inside a user
namespace:

```
root@runc:/# cat /proc/self/uid_map
         0      65536      65536
```

The output should be exactly the same.
