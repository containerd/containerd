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
	"errors"

	"github.com/containerd/containerd/api/types/runc/options"
	runtimeoptions "github.com/containerd/containerd/api/types/runtimeoptions/v1"
	"github.com/urfave/cli/v2"
)

func init() {
	RuntimeFlags = append(RuntimeFlags, &cli.StringFlag{
		Name:  "runc-binary",
		Usage: "Specify runc-compatible binary",
	}, &cli.StringFlag{
		Name:  "runc-root",
		Usage: "Specify runc-compatible root",
	}, &cli.BoolFlag{
		Name:  "runc-systemd-cgroup",
		Usage: "Start runc with systemd cgroup manager",
	})
	ContainerFlags = append(ContainerFlags, &cli.BoolFlag{
		Name:  "rootfs",
		Usage: "Use custom rootfs that is not managed by containerd snapshotter",
	}, &cli.BoolFlag{
		Name:  "no-pivot",
		Usage: "Disable use of pivot-root (linux only)",
	}, &cli.Int64Flag{
		Name:  "cpu-quota",
		Usage: "Limit CPU CFS quota",
		Value: -1,
	}, &cli.Uint64Flag{
		Name:  "cpu-period",
		Usage: "Limit CPU CFS period",
	}, &cli.StringFlag{
		Name:  "rootfs-propagation",
		Usage: "Set the propagation of the container rootfs",
	}, &cli.StringSliceFlag{
		Name:  "device",
		Usage: "File path to a device to add to the container; or a path to a directory tree of devices to add to the container",
	})
}

func getRuncOptions(context *cli.Context) (*options.Options, error) {
	runtimeOpts := &options.Options{}
	if runcBinary := context.String("runc-binary"); runcBinary != "" {
		runtimeOpts.BinaryName = runcBinary
	}
	if context.Bool("runc-systemd-cgroup") {
		if context.String("cgroup") == "" {
			// runc maps "machine.slice:foo:deadbeef" to "/machine.slice/foo-deadbeef.scope"
			return nil, errors.New("option --runc-systemd-cgroup requires --cgroup to be set, e.g. \"machine.slice:foo:deadbeef\"")
		}
		runtimeOpts.SystemdCgroup = true
	}
	if root := context.String("runc-root"); root != "" {
		runtimeOpts.Root = root
	}

	return runtimeOpts, nil
}

func RuntimeOptions(context *cli.Context) (interface{}, error) {
	// validate first
	if (context.String("runc-binary") != "" || context.Bool("runc-systemd-cgroup")) &&
		context.String("runtime") != "io.containerd.runc.v2" {
		return nil, errors.New("specifying runc-binary and runc-systemd-cgroup is only supported for \"io.containerd.runc.v2\" runtime")
	}

	if context.String("runtime") == "io.containerd.runc.v2" {
		return getRuncOptions(context)
	}

	if configPath := context.String("runtime-config-path"); configPath != "" {
		return &runtimeoptions.Options{
			ConfigPath: configPath,
		}, nil
	}

	return nil, nil
}
