/*
Copyright 2017 The Kubernetes Authors All rights reserved.

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
	// CRIContainerdSocketPath is the path to the socket which cri-containerd serves on.
	CRIContainerdSocketPath string
	// ContainerdSocketPath is the path to the containerd socket.
	ContainerdSocketPath string
	// ContainerdConnectionTimeout is the connection timeout for containerd client.
	ContainerdConnectionTimeout time.Duration
}

func NewCRIContainerdOptions() *CRIContainerdOptions {
	return &CRIContainerdOptions{}
}

// AddFlags adds cri-containerd command line options to pflag.
func (c *CRIContainerdOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.CRIContainerdSocketPath, "cri-containerd-socket",
		"/var/run/cri-containerd.sock", "Path to the socket which cri-containerd serves on.")
	fs.StringVar(&c.ContainerdSocketPath, "containerd-socket",
		"/run/containerd/containerd.sock", "Path to the containerd socket.")
	fs.DurationVar(&c.ContainerdConnectionTimeout, "containerd-connection-timeout",
		2*time.Minute, "Connection timeout for containerd client.")
}

func InitFlags() {
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()
}
