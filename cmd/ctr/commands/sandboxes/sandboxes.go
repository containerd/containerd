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

package sandboxes

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/defaults"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/sandbox"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var (
	sandboxerFlages = []cli.Flag{
		cli.StringFlag{
			Name: "sandboxer",
			Usage: "sandboxer name, required by create and delete command " +
				"(will be set to a default value of \"linux-sandbox\" if it is empty), if it is empty in list command, " +
				"it will list all sandboxes of all sandboxers",
		},
	}
	createFlags = []cli.Flag{
		cli.StringFlag{
			Name:  "config,c",
			Usage: "path to the runtime-specific spec config file",
		},
		cli.StringFlag{
			Name:  "cwd",
			Usage: "specify the working directory of the process",
		},
		cli.StringSliceFlag{
			Name:  "env",
			Usage: "specify additional container environment variables (e.g. FOO=bar)",
		},
		cli.StringFlag{
			Name:  "env-file",
			Usage: "specify additional container environment variables in a file(e.g. FOO=bar, one per line)",
		},
		cli.StringSliceFlag{
			Name:  "label",
			Usage: "specify additional labels (e.g. foo=bar)",
		},
		cli.BoolFlag{
			Name:  "net-host",
			Usage: "enable host networking for the container",
		},
		cli.BoolFlag{
			Name:  "privileged",
			Usage: "run privileged container",
		},
		cli.BoolFlag{
			Name:  "read-only",
			Usage: "set the containers filesystem as readonly",
		},
		cli.StringFlag{
			Name:  "sandboxer",
			Usage: "sandboxer name",
			Value: defaults.DefaultSandboxer,
		},
		cli.StringFlag{
			Name:  "runtime-config-path",
			Usage: "optional runtime config path",
		},
		cli.StringSliceFlag{
			Name:  "with-ns",
			Usage: "specify existing Linux namespaces to join at container runtime (format '<nstype>:<path>')",
		},
		cli.StringFlag{
			Name:  "pid-file",
			Usage: "file path to write the task's pid",
		},
		cli.IntFlag{
			Name:  "gpus",
			Usage: "add gpus to the container",
		},
		cli.BoolFlag{
			Name:  "allow-new-privs",
			Usage: "turn off OCI spec's NoNewPrivileges feature flag",
		},
		cli.Uint64Flag{
			Name:  "memory-limit",
			Usage: "memory limit (in bytes) for the container",
		},
		cli.StringSliceFlag{
			Name:  "device",
			Usage: "file path to a device to add to the container; or a path to a directory tree of devices to add to the container",
		},
		cli.BoolFlag{
			Name:  "seccomp",
			Usage: "enable the default seccomp profile",
		},
		cli.StringFlag{
			Name:  "seccomp-profile",
			Usage: "file path to custom seccomp profile. seccomp must be set to true, before using seccomp-profile",
		},
		cli.StringFlag{
			Name:  "apparmor-default-profile",
			Usage: "enable AppArmor with the default profile with the specified name, e.g. \"cri-containerd.apparmor.d\"",
		},
		cli.StringFlag{
			Name:  "apparmor-profile",
			Usage: "enable AppArmor with an existing custom profile",
		},
	}
)

// Command is the cli command for managing containers
var Command = cli.Command{
	Name:    "sandboxes",
	Usage:   "manage sandboxes",
	Aliases: []string{"s", "sandbox"},
	Flags:   sandboxerFlages,
	Subcommands: []cli.Command{
		createCommand,
		deleteCommand,
		//infoCommand,
		listCommand,
	},
}

var createCommand = cli.Command{
	Name:      "create",
	Usage:     "create sandbox",
	ArgsUsage: "[flags] SANDBOX",
	Flags:     append(createFlags, platformCreateFlags...),
	Action: func(context *cli.Context) error {
		sandboxer := context.GlobalString("sandboxer")
		if sandboxer != "" {
			sandboxer = defaults.DefaultSandboxer
		}
		id := context.Args().First()
		if context.NArg() != 1 {
			return errors.Wrap(errdefs.ErrInvalidArgument, "only sandbox id should be provided")
		}
		if id == "" {
			return errors.Wrap(errdefs.ErrInvalidArgument, "sandbox id must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		specOpts, err := generateSpecOpts(context)
		if err != nil {
			return err
		}

		var opts []containerd.NewSandboxOpt
		var s specs.Spec
		opts = append(opts, containerd.WithSandboxSpec(&s, specOpts...))
		_, err = client.NewSandbox(ctx, sandboxer, id, opts...)
		return err
	},
}

var deleteCommand = cli.Command{
	Name:      "delete",
	Usage:     "delete sandbox",
	ArgsUsage: "[flags] SANDBOX",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "force",
			Usage: "force delete sandbox even there are containers",
		},
	},
	Action: func(context *cli.Context) error {
		sandboxer := context.GlobalString("sandboxer")
		if sandboxer != "" {
			sandboxer = defaults.DefaultSandboxer
		}
		id := context.Args().First()
		if context.NArg() != 1 {
			return errors.Wrap(errdefs.ErrInvalidArgument, "only sandbox id should be provided")
		}
		if id == "" {
			return errors.Wrap(errdefs.ErrInvalidArgument, "sandbox id must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		sandboxInstance, err := client.LoadSandbox(ctx, sandboxer, id)
		if err != nil {
			return err
		}
		sandbox, err := sandboxInstance.Metadata(ctx)
		if err != nil {
			return err
		}

		if len(sandbox.Containers) > 0 && !context.Bool("force") {
			return fmt.Errorf("there are containers in sandbox, remove them first")
		}
		if err := sandboxInstance.Delete(ctx); err != nil {
			if !errdefs.IsNotFound(err) {
				return err
			}
		}
		return nil
	},
}

var listCommand = cli.Command{
	Name:      "list",
	Aliases:   []string{"ls"},
	Usage:     "list sandboxes",
	ArgsUsage: "[flags] [<filter>, ...]",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "quiet, q",
			Usage: "print only the sandbox id",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			filters       = context.Args()
			quiet         = context.Bool("quiet")
			sandboxerName = context.GlobalString("sandboxer")
		)
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		var sandboxesMap map[string][]sandbox.Sandbox
		if sandboxerName == "" {
			sandboxesMap, err = client.AllSandboxes(ctx)
			if err != nil {
				return err
			}
		} else {
			sandboxer := client.SandboxService(sandboxerName)
			if sandboxer == nil {
				return fmt.Errorf("failed to get sandboxer by name %s", sandboxerName)
			}
			sandboxes, err := sandboxer.List(ctx, filters...)
			if err != nil {
				return err
			}
			sandboxesMap = make(map[string][]sandbox.Sandbox)
			sandboxesMap[sandboxerName] = sandboxes
		}
		return printSandboxes(ctx, sandboxesMap, client, quiet)
	},
}

func generateSpecOpts(context *cli.Context) ([]containerd.SimpleSpecOpts, error) {
	var specOpts []containerd.SimpleSpecOpts
	if context.IsSet("config") {
		specOpts = append(specOpts, containerd.Simple(oci.WithSpecFromFile(context.String("config"))))
	} else {
		specOpts = append(specOpts, containerd.Simple(oci.WithDefaultSpec()))
		if context.Bool("net-host") {
			specOpts = append(specOpts, containerd.Simple(oci.WithHostNamespace(specs.NetworkNamespace)), containerd.Simple(oci.WithHostHostsFile), containerd.Simple(oci.WithHostResolvconf))
		}

		limit := context.Uint64("memory-limit")
		if limit != 0 {
			specOpts = append(specOpts, containerd.Simple(oci.WithMemoryLimit(limit)))
		}
		platformOpts, err := platformSpecOpts(context)
		if err != nil {
			return nil, err
		}
		specOpts = append(specOpts, platformOpts...)
	}
	return specOpts, nil
}

func printSandboxes(ctx context.Context, sandboxesMap map[string][]sandbox.Sandbox, client *containerd.Client, quiet bool) error {
	if quiet {
		for _, ss := range sandboxesMap {
			for _, s := range ss {
				fmt.Printf("%s\n", s.ID)
			}
		}
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 4, 8, 4, ' ', 0)
	if _, err := fmt.Fprintln(w, "SANDBOX\tSTATUS\tCONTAINERS\tADDRESS\tSANDBOXER\t"); err != nil {
		return err
	}
	for sandboxerName, sandboxes := range sandboxesMap {
		sandboxer := client.SandboxService(sandboxerName)
		for _, s := range sandboxes {
			var statusStr string
			status, err := sandboxer.Status(ctx, s.ID)
			if err != nil {
				statusStr = "error"
			} else {
				statusStr = string(status.State)
			}
			if _, err := fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t\n",
				s.ID,
				statusStr,
				len(s.Containers),
				s.TaskAddress,
				sandboxerName,
			); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}
