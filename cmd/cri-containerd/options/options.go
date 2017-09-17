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

	"github.com/BurntSushi/toml"
	"github.com/containerd/containerd"
	"github.com/spf13/pflag"
)

// configFilePathArgName is the path to the config file.
const configFilePathArgName = "config"

// ContainerdConfig contains config related to containerd
type ContainerdConfig struct {
	// ContainerdSnapshotter is the snapshotter used by containerd.
	ContainerdSnapshotter string `toml:"snapshotter"`
	// ContainerdEndpoint is the containerd endpoint path.
	ContainerdEndpoint string `toml:"endpoint"`
}

// CniConfig contains config related to cni
type CniConfig struct {
	// NetworkPluginBinDir is the directory in which the binaries for the plugin is kept.
	NetworkPluginBinDir string `toml:"bin_dir"`
	// NetworkPluginConfDir is the directory in which the admin places a CNI conf.
	NetworkPluginConfDir string `toml:"conf_dir"`
}

// Config contains cri-containerd toml config
type Config struct {
	// ContainerdConfig contains config related to containerd
	ContainerdConfig `toml:"containerd"`
	// CniConfig contains config related to cni
	CniConfig `toml:"cni"`
	// SocketPath is the path to the socket which cri-containerd serves on.
	SocketPath string `toml:"socket_path"`
	// RootDir is the root directory path for managing cri-containerd files
	// (metadata checkpoint etc.)
	RootDir string `toml:"root_dir"`
	// StreamServerAddress is the ip address streaming server is listening on.
	StreamServerAddress string `toml:"stream_server_address"`
	// StreamServerPort is the port streaming server is listening on.
	StreamServerPort string `toml:"stream_server_port"`
	// CgroupPath is the path for the cgroup that cri-containerd is placed in.
	CgroupPath string `toml:"cgroup_path"`
	// EnableSelinux indicates to enable the selinux support
	EnableSelinux bool `toml:"enable_selinux"`
	// SandboxImage is the image used by sandbox container.
	SandboxImage string `toml:"sandbox_image"`
}

// CRIContainerdOptions contains cri-containerd command line and toml options.
type CRIContainerdOptions struct {
	// Config contains cri-containerd toml config
	Config
	// Path to the TOML config file
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
	fs.StringVar(&c.ConfigFilePath, configFilePathArgName,
		"/etc/cri-containerd/config.toml", "Path to the config file.")
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
	fs.StringVar(&c.CgroupPath, "cgroup-path",
		"", "The cgroup that cri-containerd is part of. By default cri-containerd is not placed in a cgroup.")
	fs.BoolVar(&c.EnableSelinux, "selinux-enabled",
		false, "Enable selinux support.")
	fs.StringVar(&c.SandboxImage, "sandbox-image",
		"gcr.io/google_containers/pause:3.0", "The image used by sandbox container.")
}

// InitFlags must be called after adding all cli options flags are defined and
// before flags are accessed by the program. Ths fuction adds flag.CommandLine
// (the default set of command-line flags, parsed from os.Args) and then calls
// pflag.Parse().
// precedence:  commandline > configfile > default
func (c *CRIContainerdOptions) InitFlags(fs *pflag.FlagSet) error {
	fs.AddGoFlagSet(flag.CommandLine)

	commandline := os.Args[1:]
	err := fs.Parse(commandline)
	if err != nil {
		return err
	}

	// Load default config file if none provided
	_, err = toml.DecodeFile(c.ConfigFilePath, &c.Config)
	if err != nil {
		// the absence of default config file is normal case.
		if !fs.Changed(configFilePathArgName) && os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// What is the reason for applying the command line twice?
	// Because the values from command line has the highest priority.
	// So I must get the path of toml configuration file from command line,
	// it trigger the first parse.
	// The first parse generate the the default value and the value from command line at the same time.
	// But the priority of toml config value is more higher than of default value,
	// So I have not another way to insert toml config value between default value and command line value.
	// So I trigger twice parses, one for default value, one for commandline value.
	return fs.Parse(commandline)
}
