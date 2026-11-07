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

// Package sandboxfiles builds the content of the files a pod sandbox shares
// with every container of the pod: /etc/hostname, /etc/hosts, /etc/resolv.conf
// and the /dev/shm tmpfs.
//
// The package is deliberately a leaf: it has no dependency on the CRI server
// or on any sandbox implementation, so that both the in-process podsandbox
// controller and a shim that implements the Sandbox API can produce the same
// files. Where the files live and how they reach the containers is decided by
// the caller.
package sandboxfiles

import (
	"fmt"
	"strings"
)

const (
	// HostnameFile is the file name of the pod hostname file.
	HostnameFile = "hostname"
	// HostsFile is the file name of the pod hosts file.
	HostsFile = "hosts"
	// ResolvConfFile is the file name of the pod resolv.conf.
	ResolvConfFile = "resolv.conf"
	// ShmDir is the directory name of the pod /dev/shm tmpfs.
	ShmDir = "shm"

	// EtcHostname is the path of the hostname file inside a container.
	EtcHostname = "/etc/hostname"
	// EtcHosts is the path of the hosts file on the host and inside a container.
	EtcHosts = "/etc/hosts"
	// ResolvConfPath is the path of resolv.conf on the host and inside a container.
	ResolvConfPath = "/etc/resolv.conf"
	// DevShm is the path of the shared memory tmpfs on the host and inside a container.
	DevShm = "/dev/shm"

	// DefaultShmSize is the default size of the pod /dev/shm tmpfs.
	DefaultShmSize = int64(1024 * 1024 * 64)
)

// HostnameContent returns the content of the pod hostname file.
func HostnameContent(hostname string) []byte {
	return []byte(hostname + "\n")
}

// ResolvConfContent renders resolv.conf content from CRI DNS options. When no
// option is specified the result is empty.
func ResolvConfContent(servers, searches, options []string) []byte {
	var b strings.Builder

	if len(searches) > 0 {
		fmt.Fprintf(&b, "search %s\n", strings.Join(searches, " "))
	}

	if len(servers) > 0 {
		fmt.Fprintf(&b, "nameserver %s\n", strings.Join(servers, "\nnameserver "))
	}

	if len(options) > 0 {
		fmt.Fprintf(&b, "options %s\n", strings.Join(options, " "))
	}

	return []byte(b.String())
}

// ShmMountData returns the tmpfs mount data for a pod /dev/shm of the given
// size. A non-positive size falls back to DefaultShmSize.
func ShmMountData(size int64) string {
	if size <= 0 {
		size = DefaultShmSize
	}
	return fmt.Sprintf("mode=1777,size=%d", size)
}
