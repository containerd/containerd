/*
Copyright 2017 The Kubernetes Authors.

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

package options

import (
	"flag"

	"github.com/containerd/containerd"
	"github.com/spf13/pflag"
)

// CRIContainerdOptions contains cri-containerd command line options.
type CRIContainerdOptions struct {
	// SocketPath is the path to the socket which cri-containerd serves on.
	SocketPath string
	// RootDir is the root directory path for managing cri-containerd files
	// (metadata checkpoint etc.)
	RootDir string
	// PrintVersion indicates to print version information of cri-containerd.
	PrintVersion bool
	// ContainerdEndpoint is the containerd endpoint path.
	ContainerdEndpoint string
	// ContainerdSnapshotter is the snapshotter used by containerd.
	ContainerdSnapshotter string
	// NetworkPluginBinDir is the directory in which the binaries for the plugin is kept.
	NetworkPluginBinDir string
	// NetworkPluginConfDir is the directory in which the admin places a CNI conf.
	NetworkPluginConfDir string
	// StreamServerAddress is the ip address streaming server is listening on.
	StreamServerAddress string
	// StreamServerPort is the port streaming server is listening on.
	StreamServerPort string
}

// NewCRIContainerdOptions returns a reference to CRIContainerdOptions
func NewCRIContainerdOptions() *CRIContainerdOptions {
	return &CRIContainerdOptions{}
}

// AddFlags adds cri-containerd command line options to pflag.
func (c *CRIContainerdOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.SocketPath, "socket-path",
		"/var/run/cri-containerd.sock", "Path to the socket which cri-containerd serves on.")
	fs.StringVar(&c.RootDir, "root-dir",
		"/var/lib/cri-containerd", "Root directory path for cri-containerd managed files (metadata checkpoint etc).")
	fs.StringVar(&c.ContainerdEndpoint, "containerd-endpoint",
		"/run/containerd/containerd.sock", "Path to the containerd endpoint.")
	fs.StringVar(&c.ContainerdSnapshotter, "containerd-snapshotter",
		containerd.DefaultSnapshotter, "Snapshotter used by containerd.")
	fs.BoolVar(&c.PrintVersion, "version",
		false, "Print cri-containerd version information and quit.")
	fs.StringVar(&c.NetworkPluginBinDir, "network-bin-dir",
		"/opt/cni/bin", "The directory for putting network binaries.")
	fs.StringVar(&c.NetworkPluginConfDir, "network-conf-dir",
		"/etc/cni/net.d", "The directory for putting network plugin configuration files.")
	fs.StringVar(&c.StreamServerAddress, "stream-addr",
		"", "The ip address streaming server is listening on. Default host interface is used if this is empty.")
	fs.StringVar(&c.StreamServerPort, "stream-port",
		"10010", "The port streaming server is listening on.")
}

// InitFlags must be called after adding all cli options flags are defined and
// before flags are accessed by the program. Ths fuction adds flag.CommandLine
// (the default set of command-line flags, parsed from os.Args) and then calls
// pflag.Parse().
func InitFlags() {
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()
}
