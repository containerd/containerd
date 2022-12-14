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

package net

import (
	"fmt"

	gocni "github.com/containerd/go-cni"
	"github.com/containernetworking/cni/libcni"
)

type AttachmentOpt func(Args *AttachmentArgs) error
type ManagerOpt func(cfg *ManagerConfig) error
type NetworkOpt func(cfg *NetworkConfig) error

type AttachmentArgs struct {
	ContainerID    string
	NSPath         string
	IFName         string
	CapabilityArgs map[string]interface{}
	PluginArgs     map[string]string
}

type ManagerConfig struct {
	PluginDirs []string
}

type NetworkConfig struct {
	Conflist *libcni.NetworkConfigList
	Labels   map[string]string
}

func WithPluginDirs(dirs ...string) ManagerOpt {
	return func(c *ManagerConfig) error {
		c.PluginDirs = append(c.PluginDirs, dirs...)
		return nil
	}
}

func WithConflist(conflist *libcni.NetworkConfigList) NetworkOpt {
	return func(c *NetworkConfig) error {
		c.Conflist = conflist
		return nil
	}
}

func WithNetworkLabels(labels map[string]string) NetworkOpt {
	return func(c *NetworkConfig) error {
		if c.Labels == nil {
			c.Labels = make(map[string]string)
		}
		for k, v := range labels {
			c.Labels[k] = v
		}
		return nil
	}
}

func WithContainer(container string) AttachmentOpt {
	return func(c *AttachmentArgs) error {
		c.ContainerID = container
		return nil
	}
}

func WithNSPath(nsPath string) AttachmentOpt {
	return func(c *AttachmentArgs) error {
		c.NSPath = nsPath
		return nil
	}
}

func WithIFName(ifName string) AttachmentOpt {
	return func(c *AttachmentArgs) error {
		c.IFName = ifName
		return nil
	}
}

// WithCapabilityPortMap adds support for port mappings
func WithCapabilityPortMap(portMapping []gocni.PortMapping) AttachmentOpt {
	return func(c *AttachmentArgs) error {
		if c.CapabilityArgs == nil {
			c.CapabilityArgs = make(map[string]interface{})
		}
		c.CapabilityArgs["portMappings"] = portMapping
		return nil
	}
}

// WithCapabilityIPRanges adds support for ip ranges
func WithCapabilityIPRanges(ipRanges []gocni.IPRanges) AttachmentOpt {
	return func(c *AttachmentArgs) error {
		if c.CapabilityArgs == nil {
			c.CapabilityArgs = make(map[string]interface{})
		}
		c.CapabilityArgs["ipRanges"] = ipRanges
		return nil
	}
}

// WithCapabilityBandWitdh adds support for bandwidth limits
func WithCapabilityBandWidth(bandWidth gocni.BandWidth) AttachmentOpt {
	return func(c *AttachmentArgs) error {
		if c.CapabilityArgs == nil {
			c.CapabilityArgs = make(map[string]interface{})
		}
		c.CapabilityArgs["bandwidth"] = bandWidth
		return nil
	}
}

// WithCapabilityDNS adds support for dns
func WithCapabilityDNS(dns gocni.DNS) AttachmentOpt {
	return func(c *AttachmentArgs) error {
		if c.CapabilityArgs == nil {
			c.CapabilityArgs = make(map[string]interface{})
		}
		c.CapabilityArgs["dns"] = dns
		return nil
	}
}

// WithCapability support well-known capabilities
// https://www.cni.dev/docs/conventions/#well-known-capabilities
func WithCapability(name string, capability interface{}) AttachmentOpt {
	return func(c *AttachmentArgs) error {
		if c.CapabilityArgs == nil {
			c.CapabilityArgs = make(map[string]interface{})
		}
		c.CapabilityArgs[name] = capability
		return nil
	}
}

// Args
func WithLabels(labels map[string]string) AttachmentOpt {
	return func(c *AttachmentArgs) error {
		if c.PluginArgs == nil {
			c.PluginArgs = make(map[string]string)
		}
		for k, v := range labels {
			c.PluginArgs[k] = v
		}
		return nil
	}
}

func WithArgs(k, v string) AttachmentOpt {
	return func(c *AttachmentArgs) error {
		if c.PluginArgs == nil {
			c.PluginArgs = make(map[string]string)
		}
		c.PluginArgs[k] = v
		return nil
	}
}

func (a *AttachmentArgs) config() *libcni.RuntimeConf {
	c := &libcni.RuntimeConf{
		ContainerID: a.ContainerID,
		NetNS:       a.NSPath,
		IfName:      a.IFName,
	}
	for k, v := range a.PluginArgs {
		c.Args = append(c.Args, [2]string{k, v})
	}
	c.CapabilityArgs = a.CapabilityArgs
	return c
}

func (a *AttachmentArgs) validate() error {
	if len(a.ContainerID) == 0 {
		return fmt.Errorf("empty container ID")
	}
	if len(a.IFName) == 0 {
		return fmt.Errorf("empty ifname")
	}
	return nil
}

func (c *NetworkConfig) validate() error {
	if len(c.Conflist.Name) == 0 {
		return fmt.Errorf("empty network name %q", c.Conflist.Name)
	}

	if len(c.Conflist.Plugins) == 0 {
		return fmt.Errorf("zero plugins")
	}

	return nil
}
