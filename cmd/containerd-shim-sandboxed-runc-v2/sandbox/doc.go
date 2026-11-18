//go:build linux

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

// Package sandbox implements the Sandbox API (runtime.sandbox.v1) for a pod
// that has no pause container.
//
// # Configuration
//
// The shim serves the io.containerd.runc.v2 runtime type from a different
// binary, so CRI treats the handler exactly as runc (runc options, features,
// cgroup driver); the handler names the binary and the sandboxer:
//
//	[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.runc]
//	  runtime_type = "io.containerd.runc.v2"
//	  runtime_path = "/usr/local/bin/containerd-shim-sandboxed-runc-v2"
//	  sandboxer = "shim"
//	  disable_pause_image_pull = true
//
// # What a pause sandbox does today
//
// With the podsandbox controller a pod sandbox is a container: CRI pulls the
// pause image, builds an OCI spec for it (hostname, namespaces, sysctls,
// cgroup, security options), creates the pod shared files under the CRI state
// directory (<state>/sandboxes/<id>/{hostname,hosts,resolv.conf,shm}) and runs
// the pause process through the runc shim. The pause process exists only to
// keep the pod network, IPC and UTS namespaces alive: containers of the pod
// join /proc/<pause pid>/ns/{net,ipc,uts} and CRI bind-mounts the shared
// files, which it created itself, into every container.
//
// # What this package does instead
//
// The shim is the sandbox. CreateSandbox receives the CRI PodSandboxConfig
// (as the sandbox options) and the network namespace CRI created and
// configured with CNI, and sets the pod up, without a process unless the pod
// shares its PID namespace:
//
//   - Namespaces (setup.go). runc creates the namespaces as it did for the
//     pause container. "runc create" of a one-time setup container joins the
//     CRI network namespace, creates an IPC and a UTS namespace, sets the pod
//     hostname and applies the pod sysctls, and its process then waits to
//     exec its program. The shim bind-mounts /proc/<pid>/ns/{ipc,uts} of that
//     process onto files under <bundle>/ns, called pins, and deletes the
//     container. No process remains; the pins keep the namespaces alive as
//     the mounts under /var/run/netns keep the CRI network namespaces alive.
//     The shim neither creates nor pins a namespace the pod shares with the
//     host (hostNetwork, hostIPC). It does not run runc for a pod that shares
//     both with the host and does not share a PID namespace among its
//     containers.
//
//   - Shared files (bundle.go). The shim writes hostname, copies the host
//     hosts file, writes resolv.conf from the pod DNS config (or copies the
//     host one) and mounts the pod shm tmpfs, all under its bundle instead of
//     the CRI state directory, and lists them as Mounts of the sandbox spec.
//
//   - Shared PID namespace (setup.go). A pod that shares its PID namespace
//     (shareProcessNamespace) needs a durable PID 1 for it, the role pause
//     plays. The same setup container also creates the PID namespace, and
//     the shim starts it, running containerd-sandbox-init in the pod cgroup.
//     The spec names the pinned PID namespace for the containers to join.
//     Every other pod does not have a sandbox process.
//
//   - Spec (sandboxSpec in config.go). StartSandbox returns pid 0 and a
//     synthesized OCI spec that carries the namespace paths, the sysctls, the
//     shared file mounts and the annotations of the request. It describes what
//     the sandbox holds; there is no process it could describe.
//
// CRI consumes that spec where it used the pause pid before: a sandbox
// reporting pid 0 with a Linux section in its spec joins containers to the
// namespace paths of the spec (a namespace missing from the spec is the host
// one when the pod asked for it, and an error otherwise), and bind-mounts the
// shared files from the spec mount sources when nothing exists at the fixed
// CRI paths. Sandboxes with a pid, and older Sandbox API shims returning no
// spec, keep the pid derived paths unchanged.
//
// # Lifetime and cleanup
//
// One shim process serves one pod: its sandbox and, through the wrapped task
// service of containerd-shim-runc-v2 (task.go in package main), the runc tasks
// of its containers. StopSandbox releases what the sandbox holds and
// ShutdownSandbox ends the shim; CRI stops and removes the containers of a
// pod before either. Everything the shim creates lives under its bundle at
// fixed paths, so Cleanup needs no state and runs the same way on create
// failure, on stop and from "shim delete".
//
// # Not supported yet
//
// User namespaced pods need their namespaces created inside the pod user
// namespace. The setup runc container can do that, but the shim does not
// configure it yet and rejects these pods. It also rejects user.* sysctls and
// sysctls whose names contain a "." (runc maps every "." to "/"). The shim
// requires cgroup v2, and runc 1.2 or newer on Linux 5.3 or newer to hand
// over a pidfd. It does not run rootless. SELinux labels are not applied yet:
// the pod label allocation moves into the CRI layer with
// https://github.com/containerd/containerd/pull/14108, after which the shim
// applies the labels it is handed (see the TODO in CreateSandbox).
package sandbox
