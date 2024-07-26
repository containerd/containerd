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

package server

import (
	"fmt"

	"github.com/moby/sys/user/userns"
	"github.com/opencontainers/selinux/go-selinux"
	"tags.cncf.io/container-device-interface/pkg/cdi"

	"github.com/containerd/containerd/v2/pkg/cap"
	"github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/containerd/go-cni"
	"github.com/containerd/log"
)

func init() {
	var err error
	kernelSupportsRRO, err = kernelversion.GreaterEqualThan(kernelversion.KernelVersion{Kernel: 5, Major: 12})
	if err != nil {
		panic(fmt.Errorf("failed to check kernel version: %w", err))
	}
}

// initPlatform handles linux specific initialization for the CRI service.
func (c *criService) initPlatform() (err error) {
	if userns.RunningInUserNS() {
		if c.apparmorEnabled() || !c.config.RestrictOOMScoreAdj {
			log.L.Warn("Running CRI plugin in a user namespace typically requires disable_apparmor and restrict_oom_score_adj to be true")
		}
	}

	if c.config.EnableSelinux {
		if !selinux.GetEnabled() {
			log.L.Warn("Selinux is not supported")
		}
		if r := c.config.SelinuxCategoryRange; r > 0 {
			selinux.CategoryRange = uint32(r)
		}
	} else {
		selinux.SetDisabled()
	}

	pluginDirs := map[string]string{
		defaultNetworkPlugin: c.config.NetworkPluginConfDir,
	}
	for name, conf := range c.config.Runtimes {
		if conf.NetworkPluginConfDir != "" {
			pluginDirs[name] = conf.NetworkPluginConfDir
		}
	}

	networkAttachCount := 2

	if c.Config().UseInternalLoopback {
		networkAttachCount = 1
	}

	c.netPlugin = make(map[string]cni.CNI)
	for name, dir := range pluginDirs {
		max := c.config.NetworkPluginMaxConfNum
		if name != defaultNetworkPlugin {
			if m := c.config.Runtimes[name].NetworkPluginMaxConfNum; m != 0 {
				max = m
			}
		}
		// Pod needs to attach to at least loopback network and a non host network,
		// hence networkAttachCount is 2 if the CNI plugin is used and
		// 1 if the internal mechanism for setting lo to up is used.
		// If there are more network configs the pod will be attached to all the networks
		// but we will only use the ip of the default network interface as the pod IP.
		i, err := cni.New(cni.WithMinNetworkCount(networkAttachCount),
			cni.WithPluginConfDir(dir),
			cni.WithPluginMaxConfNum(max),
			cni.WithPluginDir([]string{c.config.NetworkPluginBinDir}))
		if err != nil {
			return fmt.Errorf("failed to initialize cni: %w", err)
		}
		c.netPlugin[name] = i
	}

	if c.allCaps == nil {
		c.allCaps, err = cap.Current()
		if err != nil {
			return fmt.Errorf("failed to get caps: %w", err)
		}
	}

	if c.config.EnableCDI {
		err := cdi.Configure(cdi.WithSpecDirs(c.config.CDISpecDirs...))
		if err != nil {
			return fmt.Errorf("failed to configure CDI registry")
		}
	}

	return nil
}

// cniLoadOptions returns cni load options for the linux.
func (c *criService) cniLoadOptions() []cni.Opt {
	if c.config.UseInternalLoopback {
		return []cni.Opt{cni.WithDefaultConf}
	}

	return []cni.Opt{cni.WithLoNetwork, cni.WithDefaultConf}
}
