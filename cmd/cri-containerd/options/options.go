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
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/containerd/containerd"
	"github.com/spf13/pflag"
)

// configFilePathArgName is the path to the config file.
const configFilePathArgName = "config"

// ContainerdConfig contains config related to containerd
type ContainerdConfig struct {
	// RootDir is the root directory path for containerd.
	RootDir string `toml:"root_dir"`
	// Snapshotter is the snapshotter used by containerd.
	Snapshotter string `toml:"snapshotter"`
	// Endpoint is the containerd endpoint path.
	Endpoint string `toml:"endpoint"`
	// Runtime is the runtime to use in containerd. We may support
	// other runtimes in the future.
	Runtime string `toml:"runtime"`
	// RuntimeEngine is the name of the runtime engine used by containerd.
	// Containerd default should be "runc"
	// We may support other runtime engines in the future.
	RuntimeEngine string `toml:"runtime_engine"`
	// RuntimeRoot is the directory used by containerd for runtime state.
	// Containerd default should be "/run/containerd/runc"
	RuntimeRoot string `toml:"runtime_root"`
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
	// EnableSelinux indicates to enable the selinux support.
	EnableSelinux bool `toml:"enable_selinux"`
	// SandboxImage is the image used by sandbox container.
	SandboxImage string `toml:"sandbox_image"`
	// StatsCollectPeriod is the period (in seconds) of snapshots stats collection.
	StatsCollectPeriod int `toml:"stats_collect_period"`
	// SystemdCgroup enables systemd cgroup support.
	SystemdCgroup bool `toml:"systemd_cgroup"`
}

// CRIContainerdOptions contains cri-containerd command line and toml options.
type CRIContainerdOptions struct {
	// Config contains cri-containerd toml config
	Config
	// Path to the TOML config file
	ConfigFilePath string
	// PrintVersion indicates to print version information of cri-containerd.
	PrintVersion bool
	// PrintDefaultConfig indicates to print default toml config of cri-containerd.
	PrintDefaultConfig bool
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
	fs.StringVar(&c.ContainerdConfig.RootDir, "containerd-root-dir",
		"/var/lib/containerd", "Root directory path where containerd stores persistent data.")
	fs.StringVar(&c.ContainerdConfig.Endpoint, "containerd-endpoint",
		"/run/containerd/containerd.sock", "Path to the containerd endpoint.")
	fs.StringVar(&c.ContainerdConfig.Snapshotter, "containerd-snapshotter",
		containerd.DefaultSnapshotter, "The snapshotter used by containerd.")
	fs.StringVar(&c.ContainerdConfig.Runtime, "containerd-runtime",
		"io.containerd.runtime.v1.linux", "The runtime used by containerd.")
	fs.StringVar(&c.ContainerdConfig.RuntimeEngine, "containerd-runtime-engine",
		"", "Runtime engine used by containerd. (default = \"\" uses containerd default)")
	fs.StringVar(&c.ContainerdConfig.RuntimeRoot, "containerd-runtime-root",
		"", "The directory used by containerd for runtime state. (default = \"\" uses containerd default)")
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
	fs.BoolVar(&c.EnableSelinux, "enable-selinux",
		false, "Enable selinux support. (default false)")
	fs.StringVar(&c.SandboxImage, "sandbox-image",
		"gcr.io/google_containers/pause:3.0", "The image used by sandbox container.")
	fs.IntVar(&c.StatsCollectPeriod, "stats-collect-period",
		10, "The period (in seconds) of snapshots stats collection.")
	fs.BoolVar(&c.SystemdCgroup, "systemd-cgroup",
		false, "Enables systemd cgroup support. (default false)")
	fs.BoolVar(&c.PrintDefaultConfig, "default-config",
		false, "Print default toml config of cri-containerd and quit.")
}

// InitFlags must be called after adding all cli options flags are defined and
// before flags are accessed by the program. Ths fuction adds flag.CommandLine
// (the default set of command-line flags, parsed from os.Args) and then calls
// pflag.Parse().
// precedence:  commandline > configfile > default
func (c *CRIContainerdOptions) InitFlags(fs *pflag.FlagSet) error {
	fs.AddGoFlagSet(flag.CommandLine)

	commandline := os.Args[1:]
	if err := fs.Parse(commandline); err != nil {
		return err
	}

	// Load default config file if none provided
	if _, err := toml.DecodeFile(c.ConfigFilePath, &c.Config); err != nil {
		// the absence of default config file is normal case.
		if !fs.Changed(configFilePathArgName) && os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// What is the reason for applying the command line twice?
	// Because the values from command line have the highest priority.
	// The path of toml configuration file if from the command line,
	// and triggers the first parse.
	// The first parse generates the default value and the value from command line at the same time.
	// But the priority of the toml config value is higher than the default value,
	// Without a way to insert the toml config value between the default value and the command line value.
	// We parse twice one for default value, one for commandline value.
	return fs.Parse(commandline)
}

// PrintDefaultTomlConfig print default toml config of cri-containerd.
func (c *CRIContainerdOptions) PrintDefaultTomlConfig() {
	fs := pflag.NewFlagSet("default-config", pflag.ExitOnError)

	c.AddFlags(fs)

	if err := toml.NewEncoder(os.Stdout).Encode(c.Config); err != nil {
		fmt.Println(err)
		return
	}
}
