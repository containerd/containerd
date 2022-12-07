//go:build !windows
// +build !windows

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

package commands

import (
	"github.com/urfave/cli"
)

func init() {
	ContainerFlags = append(ContainerFlags,
		cli.BoolFlag{
			Name:  "rootfs",
			Usage: "use custom rootfs that is not managed by containerd snapshotter",
		},
		cli.StringFlag{
			Name:  "rootfs-propagation",
			Usage: "set the propagation of the container rootfs",
		},
		cli.Float64Flag{
			Name:  "cpus",
			Usage: "set the CFS cpu quota",
			Value: 0.0,
		},
		cli.IntFlag{
			Name:  "cpu-shares",
			Usage: "set the cpu shares",
			Value: 1024,
		},
		cli.Int64Flag{
			Name:  "cpu-quota",
			Usage: "limit CPU CFS quota",
			Value: -1,
		},
		cli.Uint64Flag{
			Name:  "cpu-period",
			Usage: "limit CPU CFS period",
		},
		cli.StringSliceFlag{
			Name:  "device",
			Usage: "file path to a device to add to the container; or a path to a directory tree of devices to add to the container",
		},
		cli.BoolFlag{
			Name:  "privileged-without-host-devices",
			Usage: "don't pass all host devices to privileged container",
		},
		cli.StringFlag{
			Name:  "runc-binary",
			Usage: "specify runc-compatible binary",
		},
		cli.StringFlag{
			Name:  "runc-root",
			Usage: "specify runc-compatible root",
		},
		cli.BoolFlag{
			Name:  "runc-systemd-cgroup",
			Usage: "start runc with systemd cgroup manager",
		},
		cli.BoolFlag{
			Name:  "remap-labels",
			Usage: "provide the user namespace ID remapping to the snapshotter via label options; requires snapshotter support",
		},
		cli.StringFlag{
			Name:  "uidmap",
			Usage: "run inside a user namespace with the specified UID mapping range; specified with the format `container-uid:host-uid:length`",
		},
		cli.StringFlag{
			Name:  "gidmap",
			Usage: "run inside a user namespace with the specified GID mapping range; specified with the format `container-gid:host-gid:length`",
		})
}
