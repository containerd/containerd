package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var namespacesCommand = cli.Command{
	Name:  "namespaces",
	Usage: "manage namespaces",
	Subcommands: cli.Commands{
		namespacesCreateCommand,
		namespacesSetLabelsCommand,
		namespacesListCommand,
		namespacesRemoveCommand,
	},
}

var namespacesCreateCommand = cli.Command{
	Name:        "create",
	Usage:       "create a new namespace.",
	ArgsUsage:   "[flags] <name> [<key>=<value]",
	Description: "Create a new namespace. It must be unique.",
	Action: func(clicontext *cli.Context) error {
		var (
			ctx               = context.Background()
			namespace, labels = objectWithLabelArgs(clicontext)
		)

		if namespace == "" {
			return errors.New("please specify a namespace")
		}

		namespaces, err := getNamespacesService(clicontext)
		if err != nil {
			return err
		}

		if err := namespaces.Create(ctx, namespace, labels); err != nil {
			return err
		}

		return nil
	},
}

var namespacesSetLabelsCommand = cli.Command{
	Name:        "label",
	Usage:       "set and clear labels for a namespace.",
	ArgsUsage:   "[flags] <name> [<key>=<value>, ...]",
	Description: "Set and clear labels for a namespace.",
	Flags:       []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		var (
			ctx               = context.Background()
			namespace, labels = objectWithLabelArgs(clicontext)
		)

		namespaces, err := getNamespacesService(clicontext)
		if err != nil {
			return err
		}

		if namespace == "" {
			return errors.New("please specify a namespace")
		}

		for k, v := range labels {
			if err := namespaces.SetLabel(ctx, namespace, k, v); err != nil {
				return err
			}
		}

		return nil
	},
}

var namespacesListCommand = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "list namespaces.",
	ArgsUsage:   "[flags]",
	Description: "List namespaces.",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "quiet, q",
			Usage: "print only the namespace name.",
		},
	},
	Action: func(clicontext *cli.Context) error {
		var (
			ctx   = context.Background()
			quiet = clicontext.Bool("quiet")
		)

		namespaces, err := getNamespacesService(clicontext)
		if err != nil {
			return err
		}

		nss, err := namespaces.List(ctx)
		if err != nil {
			return err
		}

		if quiet {
			for _, ns := range nss {
				fmt.Println(ns)
			}
		} else {

			tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
			fmt.Fprintln(tw, "NAME\tLABELS\t")

			for _, ns := range nss {
				labels, err := namespaces.Labels(ctx, ns)
				if err != nil {
					return err
				}

				var labelStrings []string
				for k, v := range labels {
					labelStrings = append(labelStrings, strings.Join([]string{k, v}, "="))
				}
				sort.Strings(labelStrings)

				fmt.Fprintf(tw, "%v\t%v\t\n", ns, strings.Join(labelStrings, ","))
			}

			return tw.Flush()
		}

		return nil
	},
}

var namespacesRemoveCommand = cli.Command{
	Name:        "remove",
	Aliases:     []string{"rm"},
	Usage:       "remove one or more namespaces",
	ArgsUsage:   "[flags] <name> [<name>, ...]",
	Description: "Remove one or more namespaces. For now, the namespace must be empty.",
	Action: func(clicontext *cli.Context) error {
		var (
			ctx     = context.Background()
			exitErr error
		)

		namespaces, err := getNamespacesService(clicontext)
		if err != nil {
			return err
		}

		for _, target := range clicontext.Args() {
			if err := namespaces.Delete(ctx, target); err != nil {
				if !errdefs.IsNotFound(err) {
					if exitErr == nil {
						exitErr = errors.Wrapf(err, "unable to delete %v", target)
					}
					log.G(ctx).WithError(err).Errorf("unable to delete %v", target)
					continue
				}

			}

			fmt.Println(target)
		}

		return exitErr

	},
}
