# vsock [![Test Status](https://github.com/mdlayher/vsock/workflows/Linux%20Test/badge.svg)](https://github.com/mdlayher/vsock/actions) [![Go Reference](https://pkg.go.dev/badge/github.com/mdlayher/vsock.svg)](https://pkg.go.dev/github.com/mdlayher/vsock)  [![Go Report Card](https://goreportcard.com/badge/github.com/mdlayher/vsock)](https://goreportcard.com/report/github.com/mdlayher/vsock)

Package `vsock` provides access to Linux VM sockets (`AF_VSOCK`) for
communication between a hypervisor and its virtual machines.  MIT Licensed.

For more information about VM sockets, check out my blog about
[Linux VM sockets in Go](https://mdlayher.com/blog/linux-vm-sockets-in-go/).

## Stability

See the [CHANGELOG](./CHANGELOG.md) file for a description of changes between
releases.

This package has a stable v1 API and any future breaking changes will prompt
the release of a new major version. Features and bug fixes will continue to
occur in the v1.x.x series.

In order to reduce the maintenance burden, this package is only supported on
Go 1.12+. Older versions of Go lack critical features and APIs which are
necessary for this package to function correctly.

**If you depend on this package in your applications, please use Go modules.**

## Requirements

**It's possible these requirements are out of date. PRs are welcome.**

To make use of VM sockets with QEMU and virtio-vsock, you must have:

- a Linux hypervisor with kernel 4.8+
- a Linux virtual machine on that hypervisor with kernel 4.8+
- QEMU 2.8+ on the hypervisor, running the virtual machine

Before using VM sockets, following modules must be removed on hypervisor:

- `modprobe -r vmw_vsock_vmci_transport`
- `modprobe -r vmw_vsock_virtio_transport_common`
- `modprobe -r vsock`

Once removed, `vhost_vsock` module needs to be enabled on hypervisor:

- `modprobe vhost_vsock`

On VM, you have to enable `vmw_vsock_virtio_transport` module.  This module should automatically load during boot when the vsock device is detected.

To utilize VM sockets, VM needs to be powered on with following `-device` flag:

- `-device vhost-vsock-pci,id=vhost-vsock-pci0,guest-cid=3`

Check out the
[QEMU wiki page on virtio-vsock](http://wiki.qemu-project.org/Features/VirtioVsock)
for more details.  More detail on setting up this environment will be provided
in the future.
