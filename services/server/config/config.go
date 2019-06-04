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

package config

import (
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/plugin"
	"github.com/pkg/errors"
)

// Config provides containerd configuration data for the server
type Config struct {
	// Version of the config file
	Version int `toml:"version"`
	// Root is the path to a directory where containerd will store persistent data
	Root string `toml:"root"`
	// State is the path to a directory where containerd will store transient data
	State string `toml:"state"`
	// GRPC configuration settings
	GRPC GRPCConfig `toml:"grpc"`
	// Debug and profiling settings
	Debug Debug `toml:"debug"`
	// Metrics and monitoring settings
	Metrics MetricsConfig `toml:"metrics"`
	// DisabledPlugins are IDs of plugins to disable. Disabled plugins won't be
	// initialized and started.
	DisabledPlugins []string `toml:"disabled_plugins"`
	// Plugins provides plugin specific configuration for the initialization of a plugin
	Plugins map[string]toml.Primitive `toml:"plugins"`
	// OOMScore adjust the containerd's oom score
	OOMScore int `toml:"oom_score"`
	// Cgroup specifies cgroup information for the containerd daemon process
	Cgroup CgroupConfig `toml:"cgroup"`
	// ProxyPlugins configures plugins which are communicated to over GRPC
	ProxyPlugins map[string]ProxyPlugin `toml:"proxy_plugins"`

	md toml.MetaData
}

// GetVersion returns the config file's version
func (c *Config) GetVersion() int {
	if c.Version == 0 {
		return 1
	}
	return c.Version
}

// ValidateV2 validates the config for a v2 file
func (c *Config) ValidateV2() error {
	if c.GetVersion() != 2 {
		return nil
	}
	for _, p := range c.DisabledPlugins {
		if len(strings.Split(p, ".")) < 4 {
			return errors.Errorf("invalid disabled plugin URI %q expect io.containerd.x.vx", p)
		}
	}
	for p := range c.Plugins {
		if len(strings.Split(p, ".")) < 4 {
			return errors.Errorf("invalid plugin key URI %q expect io.containerd.x.vx", p)
		}
	}
	for p := range c.ProxyPlugins {
		if len(strings.Split(p, ".")) < 4 {
			return errors.Errorf("invalid proxy plugin key URI %q expect io.containerd.x.vx", p)
		}
	}
	return nil
}

// GRPCConfig provides GRPC configuration for the socket
type GRPCConfig struct {
	Address        string `toml:"address"`
	UID            int    `toml:"uid"`
	GID            int    `toml:"gid"`
	MaxRecvMsgSize int    `toml:"max_recv_message_size"`
	MaxSendMsgSize int    `toml:"max_send_message_size"`
}

// Debug provides debug configuration
type Debug struct {
	Address string `toml:"address"`
	UID     int    `toml:"uid"`
	GID     int    `toml:"gid"`
	Level   string `toml:"level"`
}

// MetricsConfig provides metrics configuration
type MetricsConfig struct {
	Address       string `toml:"address"`
	GRPCHistogram bool   `toml:"grpc_histogram"`
}

// CgroupConfig provides cgroup configuration
type CgroupConfig struct {
	Path string `toml:"path"`
}

// ProxyPlugin provides a proxy plugin configuration
type ProxyPlugin struct {
	Type    string `toml:"type"`
	Address string `toml:"address"`
}

// Decode unmarshals a plugin specific configuration by plugin id
func (c *Config) Decode(p *plugin.Registration) (interface{}, error) {
	id := p.URI()
	if c.GetVersion() == 1 {
		id = p.ID
	}
	data, ok := c.Plugins[id]
	if !ok {
		return p.Config, nil
	}
	if err := c.md.PrimitiveDecode(data, p.Config); err != nil {
		return nil, err
	}
	return p.Config, nil
}

// LoadConfig loads the containerd server config from the provided path
func LoadConfig(path string, v *Config) error {
	if v == nil {
		return errors.Wrapf(errdefs.ErrInvalidArgument, "argument v must not be nil")
	}
	md, err := toml.DecodeFile(path, v)
	if err != nil {
		return err
	}
	v.md = md
	return v.ValidateV2()
}

// V1DisabledFilter matches based on ID
func V1DisabledFilter(list []string) plugin.DisableFilter {
	set := make(map[string]struct{}, len(list))
	for _, l := range list {
		set[l] = struct{}{}
	}
	return func(r *plugin.Registration) bool {
		_, ok := set[r.ID]
		return ok
	}
}

// V2DisabledFilter matches based on URI
func V2DisabledFilter(list []string) plugin.DisableFilter {
	set := make(map[string]struct{}, len(list))
	for _, l := range list {
		set[l] = struct{}{}
	}
	return func(r *plugin.Registration) bool {
		_, ok := set[r.URI()]
		return ok
	}
}
