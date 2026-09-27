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

package run

import (
	"context"
	"errors"
	"strings"

	"github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	"github.com/containerd/console"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/netns"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/log"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli/v3"
)

var platformRunFlags = []cli.Flag{
	&cli.BoolFlag{
		Name:  "isolated",
		Usage: "Run the container with vm isolation",
	},
	&cli.BoolFlag{
		Name:  "scrub-logs",
		Usage: "Scrub sensitive information from the shim logs (Windows only)",
	},
}

// NewContainer creates a new container
func NewContainer(ctx context.Context, client *containerd.Client, cmd *cli.Command) (containerd.Container, error) {
	var (
		id    string
		opts  []oci.SpecOpts
		cOpts []containerd.NewContainerOpts
		spec  containerd.NewContainerOpts

		config = cmd.IsSet("config")
	)

	if sandbox := cmd.String("sandbox"); sandbox != "" {
		cOpts = append(cOpts, containerd.WithSandbox(sandbox))
	}

	if config {
		id = cmd.Args().First()
		opts = append(opts, oci.WithSpecFromFile(cmd.String("config")))
		cOpts = append(cOpts, containerd.WithContainerLabels(commands.LabelArgs(cmd.StringSlice("label"))))
	} else {
		var (
			ref  = cmd.Args().First()
			args = cmd.Args().Slice()[2:]
		)

		id = cmd.Args().Get(1)
		snapshotter := cmd.String("snapshotter")
		if snapshotter == "windows-lcow" {
			opts = append(opts, oci.WithDefaultSpecForPlatform("linux/amd64"))
			// Clear the rootfs section.
			opts = append(opts, oci.WithRootFSPath(""))
		} else {
			opts = append(opts, oci.WithDefaultSpec())
			opts = append(opts, oci.WithWindowNetworksAllowUnqualifiedDNSQuery())
			opts = append(opts, oci.WithWindowsIgnoreFlushesDuringBoot())
		}
		if ef := cmd.String("env-file"); ef != "" {
			opts = append(opts, oci.WithEnvFile(ef))
		}
		opts = append(opts, oci.WithEnv(cmd.StringSlice("env")))
		opts = append(opts, withMounts(cmd))

		image, err := client.GetImage(ctx, ref)
		if err != nil {
			return nil, err
		}
		unpacked, err := image.IsUnpacked(ctx, snapshotter)
		if err != nil {
			return nil, err
		}
		if !unpacked {
			if err := image.Unpack(ctx, snapshotter, containerd.WithUnpackApplyOpts(diff.WithSyncFs(cmd.Bool("sync-fs")))); err != nil {
				return nil, err
			}
		}
		opts = append(opts, oci.WithImageConfig(image))
		labels := buildLabels(commands.LabelArgs(cmd.StringSlice("label")), image.Labels())
		cOpts = append(cOpts,
			containerd.WithImage(image),
			containerd.WithImageConfigLabels(image),
			containerd.WithSnapshotter(snapshotter),
			containerd.WithNewSnapshot(
				id,
				image,
				snapshots.WithLabels(commands.LabelArgs(cmd.StringSlice("snapshotter-label")))),
			containerd.WithAdditionalContainerLabels(labels))

		if len(args) > 0 {
			opts = append(opts, oci.WithProcessArgs(args...))
		}
		if cwd := cmd.String("cwd"); cwd != "" {
			opts = append(opts, oci.WithProcessCwd(cwd))
		}
		if user := cmd.String("user"); user != "" {
			opts = append(opts, oci.WithUser(user))
		}
		if cmd.Bool("tty") {
			opts = append(opts, oci.WithTTY)

			con := console.Current()
			size, err := con.Size()
			_ = con.Close()
			if err != nil {
				log.L.WithError(err).Error("console size")
			}
			opts = append(opts, oci.WithTTYSize(int(size.Width), int(size.Height)))
		}
		if cmd.Bool("net-host") {
			return nil, errors.New("cannot use host mode networking with Windows containers")
		}
		if cmd.Bool("cni") {
			ns, err := netns.NewNetNS("")
			if err != nil {
				return nil, err
			}
			opts = append(opts, oci.WithWindowsNetworkNamespace(ns.GetPath()))
		}
		if cmd.Bool("isolated") {
			opts = append(opts, oci.WithWindowsHyperV)
		}
		limit := cmd.Uint64("memory-limit")
		if limit != 0 {
			opts = append(opts, oci.WithMemoryLimit(limit))
		}
		ccount := cmd.Uint64("cpu-count")
		if ccount != 0 {
			opts = append(opts, oci.WithWindowsCPUCount(ccount))
		}
		cshares := cmd.Uint64("cpu-shares")
		if cshares != 0 {
			opts = append(opts, oci.WithWindowsCPUShares(uint16(cshares)))
		}
		cmax := cmd.Uint64("cpu-max")
		if cmax != 0 {
			opts = append(opts, oci.WithWindowsCPUMaximum(uint16(cmax)))
		}
		for _, dev := range cmd.StringSlice("device") {
			idType, devID, ok := strings.Cut(dev, "://")
			if !ok {
				return nil, errors.New("devices must be in the format IDType://ID")
			}
			if idType == "" {
				return nil, errors.New("devices must have a non-empty IDType")
			}
			opts = append(opts, oci.WithWindowsDevice(idType, devID))
		}
	}

	if cmd.Bool("cni") {
		cniMeta := &commands.NetworkMetaData{EnableCni: true}
		cOpts = append(cOpts, containerd.WithContainerExtension(commands.CtrCniMetadataExtension, cniMeta))
	}

	runtime := cmd.String("runtime")
	var runtimeOpts any
	if runtime == "io.containerd.runhcs.v1" {
		opts := &options.Options{
			Debug: cmd.Bool("debug"),
		}
		if cmd.IsSet("scrub-logs") {
			opts.ScrubLogs = new(cmd.Bool("scrub-logs"))
		}
		runtimeOpts = opts
	}
	cOpts = append(cOpts, containerd.WithRuntime(runtime, runtimeOpts))

	var s specs.Spec
	spec = containerd.WithSpec(&s, opts...)

	cOpts = append(cOpts, spec)

	return client.NewContainer(ctx, id, cOpts...)
}

func getNetNSPath(ctx context.Context, t containerd.Task) (string, error) {
	s, err := t.Spec(ctx)
	if err != nil {
		return "", err
	}
	if s.Windows == nil || s.Windows.Network == nil {
		return "", nil
	}
	return s.Windows.Network.NetworkNamespace, nil
}
