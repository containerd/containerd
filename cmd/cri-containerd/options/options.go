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
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/containerd/containerd"
	"github.com/spf13/pflag"
)

//Config contains cri-containerd toml config
type Config struct {
	// SocketPath is the path to the socket which cri-containerd serves on.
	SocketPath string `toml: "socketpath"`
	// RootDir is the root directory path for managing cri-containerd files
	// (metadata checkpoint etc.)
	RootDir string `toml: "rootdir"`
	// ContainerdSnapshotter is the snapshotter used by containerd.
	ContainerdSnapshotter string `toml: "containerdsnapshotter"`
	// ContainerdEndpoint is the containerd endpoint path.
	ContainerdEndpoint string `toml: "containerdendpoint"`
	// ContainerdConnectionTimeout is the connection timeout for containerd client.
	ContainerdConnectionTimeout time.Duration `toml: "containerdconnectiontimeout"`
	// NetworkPluginBinDir is the directory in which the binaries for the plugin is kept.
	NetworkPluginBinDir string `toml: "networkpluginbindir"`
	// NetworkPluginConfDir is the directory in which the admin places a CNI conf.
	NetworkPluginConfDir string `toml: "networkpluginconfdir"`
	// StreamServerAddress is the ip address streaming server is listening on.
	StreamServerAddress string `toml: "streamserveraddress"`
	// StreamServerPort is the port streaming server is listening on.
	StreamServerPort string `toml: "streamserverport"`
	// CgroupPath is the path for the cgroup that cri-containerd is placed in.
	CgroupPath string `toml: "cgrouppath"`
	// EnableSelinux indicates to enable the selinux support
	EnableSelinux bool `toml: "enableselinux"`
}

// CRIContainerdOptions contains cri-containerd command line and toml options.
type CRIContainerdOptions struct {
	//Config contains cri-containerd toml config
	Config

	//Path to the TOML config file
	ConfigFilePath string

	// PrintVersion indicates to print version information of cri-containerd.
	PrintVersion bool
}

// NewCRIContainerdOptions returns a reference to CRIContainerdOptions
func NewCRIContainerdOptions() *CRIContainerdOptions {
	return &CRIContainerdOptions{}
}

// AddFlags adds cri-containerd command line options to pflag.
func (c *CRIContainerdOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.ConfigFilePath, "config-file-path",
		"/etc/cri-containerd/config.toml", "Path to the config file")
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
	fs.StringVar(&c.CgroupPath, "cgroup-path", "", "The cgroup that cri-containerd is part of. By default cri-containerd is not placed in a cgroup")
	fs.BoolVar(&c.EnableSelinux, "selinux-enabled",
		false, "Enable selinux support.")
}

// InitFlags must be called after adding all cli options flags are defined and
// before flags are accessed by the program. Ths fuction adds flag.CommandLine
// (the default set of command-line flags, parsed from os.Args) and then calls
// pflag.Parse().
// precedence:  commandline > configfile > default
func (c *CRIContainerdOptions) InitFlags(fs *pflag.FlagSet) {
	fs.AddGoFlagSet(flag.CommandLine)

	commandline := os.Args[1:]
	fs.Parse(commandline) //this time:  config = default + commandline(on top)

	err := loadConfigFile(c.ConfigFilePath, &c.Config) //config = default + commandline + configfile(on top)
	if err != nil {
		return
	}

	fs.Parse(commandline) //config = default + commandline + configfile + commandline(on top)
}

func loadConfigFile(fpath string, v *Config) error {
	if v == nil {
		v = &Config{}
	}

	_, err := toml.DecodeFile(fpath, v)
	if err != nil {
		return err
	}
	return nil
}
