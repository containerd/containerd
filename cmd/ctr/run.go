package main

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/execution"
	mounttypes "github.com/containerd/containerd/api/types/mount"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/snapshot"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var runCommand = cli.Command{
	Name:      "run",
	Usage:     "run a container",
	ArgsUsage: "IMAGE [COMMAND] [ARG...]",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "id",
			Usage: "id of the container",
		},
		cli.BoolFlag{
			Name:  "tty,t",
			Usage: "allocate a TTY for the container",
		},
		cli.StringFlag{
			Name:  "rootfs",
			Usage: "path to rootfs",
		},
		cli.StringFlag{
			Name:  "runtime",
			Usage: "runtime name (linux, windows, vmware-linux)",
			Value: "linux",
		},
		cli.StringFlag{
			Name:  "runtime-config",
			Usage: "set the OCI config file for the container",
		},
		cli.BoolFlag{
			Name:  "readonly",
			Usage: "set the containers filesystem as readonly",
		},
		cli.BoolFlag{
			Name:  "net-host",
			Usage: "enable host networking for the container",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			err         error
			mounts      []containerd.Mount
			imageConfig ocispec.Image

			ctx = gocontext.Background()
			id  = context.String("id")
		)
		if id == "" {
			return errors.New("container id must be provided")
		}
		containers, err := getExecutionService(context)
		if err != nil {
			return err
		}
		tmpDir, err := getTempDir(id)
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		events, err := containers.Events(ctx, &execution.EventsRequest{})
		if err != nil {
			return err
		}

		content, err := getContentStore(context)
		if err != nil {
			return err
		}

		snapshotter, err := getSnapshotter(context)
		if err != nil {
			return err
		}

		imageStore, err := getImageStore(context)
		if err != nil {
			return errors.Wrap(err, "failed resolving image store")
		}

		if runtime.GOOS != "windows" && context.String("rootfs") == "" {
			ref := context.Args().First()

			image, err := imageStore.Get(ctx, ref)
			if err != nil {
				return errors.Wrapf(err, "could not resolve %q", ref)
			}
			// let's close out our db and tx so we don't hold the lock whilst running.

			diffIDs, err := image.RootFS(ctx, content)
			if err != nil {
				return err
			}

			mounts, err = snapshotter.Prepare(ctx, id, identity.ChainID(diffIDs).String())
			if err != nil {
				if !snapshot.IsExist(err) {
					return err
				}
				mounts, err = snapshotter.Mounts(ctx, id)
				if err != nil {
					return err
				}
			}

			ic, err := image.Config(ctx, content)
			if err != nil {
				return err
			}
			switch ic.MediaType {
			case ocispec.MediaTypeImageConfig, images.MediaTypeDockerSchema2Config:
				r, err := content.Reader(ctx, ic.Digest)
				if err != nil {
					return err
				}
				if err := json.NewDecoder(r).Decode(&imageConfig); err != nil {
					r.Close()
					return err
				}
				r.Close()
			default:
				return fmt.Errorf("unknown image config media type %s", ic.MediaType)
			}
		} else {
			// TODO: get the image / rootfs through the API once windows has a snapshotter
		}

		create, err := newCreateRequest(context, &imageConfig.Config, id, tmpDir, context.String("rootfs"))
		if err != nil {
			return err
		}
		for _, m := range mounts {
			create.Rootfs = append(create.Rootfs, &mounttypes.Mount{
				Type:    m.Type,
				Source:  m.Source,
				Options: m.Options,
			})

		}
		var con console.Console
		if create.Terminal {
			con = console.Current()
			defer con.Reset()
			if err := con.SetRaw(); err != nil {
				return err
			}
		}

		fwg, err := prepareStdio(create.Stdin, create.Stdout, create.Stderr, create.Terminal)
		if err != nil {
			return err
		}
		response, err := containers.Create(ctx, create)
		if err != nil {
			return err
		}
		if create.Terminal {
			if err := handleConsoleResize(ctx, containers, response.ID, response.Pid, con); err != nil {
				logrus.WithError(err).Error("console resize")
			}
		} else {
			sigc := forwardAllSignals(containers, id)
			defer stopCatch(sigc)
		}
		if _, err := containers.Start(ctx, &execution.StartRequest{
			ID: response.ID,
		}); err != nil {
			return err
		}
		// Ensure we read all io only if container started successfully.
		defer fwg.Wait()

		status, err := waitContainer(events, response.ID, response.Pid)
		if err != nil {
			return err
		}
		if _, err := containers.Delete(ctx, &execution.DeleteRequest{
			ID: response.ID,
		}); err != nil {
			return err
		}
		if status != 0 {
			return cli.NewExitError("", int(status))
		}
		return nil
	},
}
