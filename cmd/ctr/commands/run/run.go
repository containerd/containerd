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
	"encoding/csv"
	"errors"
	"fmt"
	"strings"

	"github.com/containerd/console"
	gocni "github.com/containerd/go-cni"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli/v2"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/tasks"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/cio"
	clabels "github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
)

func withMounts(cliContext *cli.Context) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, container *containers.Container, s *specs.Spec) error {
		mounts := make([]specs.Mount, 0)
		dests := make([]string, 0)
		for _, mount := range cliContext.StringSlice("mount") {
			m, err := parseMountFlag(mount)
			if err != nil {
				return err
			}
			mounts = append(mounts, m)
			dests = append(dests, m.Destination)
		}
		return oci.Compose(oci.WithoutMounts(dests...), oci.WithMounts(mounts))(ctx, client, container, s)
	}
}

// parseMountFlag parses a mount string in the form "type=foo,source=/path,destination=/target,options=rbind:rw"
func parseMountFlag(m string) (specs.Mount, error) {
	mount := specs.Mount{}
	r := csv.NewReader(strings.NewReader(m))

	fields, err := r.Read()
	if err != nil {
		return mount, err
	}

	for _, field := range fields {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			return mount, errors.New("invalid mount specification: expected key=val")
		}

		switch key {
		case "type":
			mount.Type = val
		case "source", "src":
			mount.Source = val
		case "destination", "dst":
			mount.Destination = val
		case "options":
			mount.Options = strings.Split(val, ":")
		default:
			return mount, fmt.Errorf("mount option %q not supported", key)
		}
	}

	return mount, nil
}

// Command runs a container
var Command = &cli.Command{
	Name:      "run",
	Usage:     "Run a container",
	ArgsUsage: "[flags] Image|RootFS ID [COMMAND] [ARG...]",
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:  "rm",
			Usage: "Remove the container after running, cannot be used with --detach",
		},
		&cli.BoolFlag{
			Name:  "null-io",
			Usage: "Send all IO to /dev/null",
		},
		&cli.StringFlag{
			Name:  "log-uri",
			Usage: "Log uri",
		},
		&cli.BoolFlag{
			Name:    "detach",
			Aliases: []string{"d"},
			Usage:   "Detach from the task after it has started execution, cannot be used with --rm",
		},
		&cli.StringFlag{
			Name:  "fifo-dir",
			Usage: "Directory used for storing IO FIFOs",
		},
		&cli.StringFlag{
			Name:  "cgroup",
			Usage: "Cgroup path (To disable use of cgroup, set to \"\" explicitly)",
		},
		&cli.StringFlag{
			Name:  "platform",
			Usage: "Run image for specific platform",
		},
		&cli.BoolFlag{
			Name:  "cni",
			Usage: "Enable cni networking for the container",
		},
		&cli.BoolFlag{
			Name:  "sync-fs",
			Usage: "Synchronize the underlying filesystem containing files when unpack images, false by default",
		},
	}, append(platformRunFlags,
		append(commands.RuntimeFlags,
			append(append(commands.SnapshotterFlags, []cli.Flag{commands.SnapshotterLabels}...),
				commands.ContainerFlags...)...)...)...),
	Action: func(cliContext *cli.Context) error {
		var (
			err error
			id  string
			ref string

			rm        = cliContext.Bool("rm")
			tty       = cliContext.Bool("tty")
			detach    = cliContext.Bool("detach")
			config    = cliContext.IsSet("config")
			enableCNI = cliContext.Bool("cni")
		)

		if config {
			id = cliContext.Args().First()
			if cliContext.NArg() > 1 {
				return errors.New("with spec config file, only container id should be provided")
			}
		} else {
			id = cliContext.Args().Get(1)
			ref = cliContext.Args().First()

			if ref == "" {
				return errors.New("image ref must be provided")
			}
		}
		if id == "" {
			return errors.New("container id must be provided")
		}
		if rm && detach {
			return errors.New("flags --detach and --rm cannot be specified together")
		}

		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		container, err := NewContainer(ctx, client, cliContext)
		if err != nil {
			return err
		}
		if rm && !detach {
			defer func() {
				if err := container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
					log.L.WithError(err).Error("failed to cleanup container")
				}
			}()
		}
		var con console.Console
		if tty {
			con = console.Current()
			defer con.Reset()
			if err := con.SetRaw(); err != nil {
				return err
			}
		}
		var network gocni.CNI
		if enableCNI {
			if network, err = gocni.New(gocni.WithDefaultConf); err != nil {
				return err
			}
		}

		opts := tasks.GetNewTaskOpts(cliContext)
		ioOpts := []cio.Opt{cio.WithFIFODir(cliContext.String("fifo-dir"))}
		task, err := tasks.NewTask(ctx, client, container, cliContext.String("checkpoint"), con, cliContext.Bool("null-io"), cliContext.String("log-uri"), ioOpts, opts...)
		if err != nil {
			return err
		}

		var statusC <-chan containerd.ExitStatus
		if !detach {
			defer func() {
				if enableCNI {
					if err := network.Remove(ctx, commands.FullID(ctx, container), ""); err != nil {
						log.L.WithError(err).Error("failed to remove network")
					}
				}

				if _, err := task.Delete(ctx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
					log.L.WithError(err).Error("failed to cleanup task")
				}
			}()

			if statusC, err = task.Wait(ctx); err != nil {
				return err
			}
		}
		if cliContext.IsSet("pid-file") {
			if err := commands.WritePidFile(cliContext.String("pid-file"), int(task.Pid())); err != nil {
				return err
			}
		}
		if enableCNI {
			netNsPath, err := getNetNSPath(ctx, task)
			if err != nil {
				return err
			}

			if _, err := network.Setup(ctx, commands.FullID(ctx, container), netNsPath); err != nil {
				return err
			}
		}
		if err := task.Start(ctx); err != nil {
			return err
		}
		if detach {
			return nil
		}
		if tty {
			if err := tasks.HandleConsoleResize(ctx, task, con); err != nil {
				log.L.WithError(err).Error("console resize")
			}
		} else {
			sigc := commands.ForwardAllSignals(ctx, task)
			defer commands.StopCatch(sigc)
		}
		status := <-statusC
		code, _, err := status.Result()
		if err != nil {
			return err
		}
		if _, err := task.Delete(ctx); err != nil {
			return err
		}
		if code != 0 {
			return cli.Exit("", int(code))
		}
		return nil
	},
}

// buildLabels builds the labels from command line labels and the image labels
func buildLabels(cmdLabels, imageLabels map[string]string) map[string]string {
	labels := make(map[string]string)
	for k, v := range imageLabels {
		if err := clabels.Validate(k, v); err == nil {
			labels[k] = v
		} else {
			// In case the image label is invalid, we output a warning and skip adding it to the
			// container.
			log.L.WithError(err).Warnf("unable to add image label with key %s to the container", k)
		}
	}
	// labels from the command line will override image and the initial image config labels
	for k, v := range cmdLabels {
		labels[k] = v
	}
	return labels
}
