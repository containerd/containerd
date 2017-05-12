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
	"time"

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
	// ContainerdConnectionTimeout is the connection timeout for containerd client.
	ContainerdConnectionTimeout time.Duration
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
	fs.DurationVar(&c.ContainerdConnectionTimeout, "containerd-connection-timeout",
		2*time.Minute, "Connection timeout for containerd client.")
	fs.BoolVar(&c.PrintVersion, "version",
		false, "Print cri-containerd version information and quit.")
}

// InitFlags must be called after adding all cli options flags are defined and
// before flags are accessed by the program. Ths fuction adds flag.CommandLine
// (the default set of command-line flags, parsed from os.Args) and then calls
// pflag.Parse().
func InitFlags() {
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()
}
