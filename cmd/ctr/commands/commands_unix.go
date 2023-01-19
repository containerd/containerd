//go:build !windows

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
	ContainerFlags = append(ContainerFlags, cli.BoolFlag{
		Name:  "rootfs",
		Usage: "Use custom rootfs that is not managed by containerd snapshotter",
	}, cli.BoolFlag{
		Name:  "no-pivot",
		Usage: "Disable use of pivot-root (linux only)",
	}, cli.Int64Flag{
		Name:  "cpu-quota",
		Usage: "Limit CPU CFS quota",
		Value: -1,
	}, cli.Uint64Flag{
		Name:  "cpu-period",
		Usage: "Limit CPU CFS period",
	}, cli.StringFlag{
		Name:  "rootfs-propagation",
		Usage: "Set the propagation of the container rootfs",
	}, cli.StringSliceFlag{
		Name:  "device",
		Usage: "File path to a device to add to the container; or a path to a directory tree of devices to add to the container",
	})
}
