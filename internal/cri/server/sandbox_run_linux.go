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
	"syscall"

	"github.com/containerd/containerd/v2/pkg/netns"
	"github.com/containerd/containerd/v2/pkg/sys"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criService) bringUpLoopback(netns string) error {
	if err := ns.WithNetNSPath(netns, func(_ ns.NetNS) error {
		link, err := netlink.LinkByName("lo")
		if err != nil {
			return err
		}
		return netlink.LinkSetUp(link)
	}); err != nil {
		return fmt.Errorf("error setting loopback interface up: %w", err)
	}
	return nil
}

func (c *criService) setupNetnsWithinUserns(netnsMountDir string, opt *runtime.UserNamespace) (*netns.NetNS, error) {
	if opt.GetMode() != runtime.NamespaceMode_POD {
		return nil, fmt.Errorf("required pod-level user namespace setting")
	}

	uidMaps := opt.GetUids()
	if len(uidMaps) != 1 {
		return nil, fmt.Errorf("required only one uid mapping, but got %d uid mapping(s)", len(uidMaps))
	}
	if uidMaps[0] == nil {
		return nil, fmt.Errorf("required only one uid mapping, but got empty uid mapping")
	}

	gidMaps := opt.GetGids()
	if len(gidMaps) != 1 {
		return nil, fmt.Errorf("required only one gid mapping, but got %d gid mapping(s)", len(gidMaps))
	}
	if gidMaps[0] == nil {
		return nil, fmt.Errorf("required only one gid mapping, but got empty gid mapping")
	}

	var netNs *netns.NetNS
	var err error
	uerr := sys.UnshareAfterEnterUserns(
		fmt.Sprintf("%d:%d:%d", uidMaps[0].ContainerId, uidMaps[0].HostId, uidMaps[0].Length),
		fmt.Sprintf("%d:%d:%d", gidMaps[0].ContainerId, gidMaps[0].HostId, gidMaps[0].Length),
		syscall.CLONE_NEWNET,
		func(pid int) error {
			netNs, err = netns.NewNetNSFromPID(netnsMountDir, uint32(pid))
			if err != nil {
				return fmt.Errorf("failed to mount netns from pid %d: %w", pid, err)
			}
			return nil
		},
	)
	if uerr != nil {
		return nil, uerr
	}
	return netNs, nil
}
