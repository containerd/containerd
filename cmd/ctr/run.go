package main

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"runtime"

	"golang.org/x/sys/unix"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/api/services/execution"
	rootfsapi "github.com/containerd/containerd/api/services/rootfs"
	"github.com/containerd/containerd/images"
	"github.com/crosbymichael/console"
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
			Name:  "runtime",
			Usage: "runtime name (linux, windows, vmware-linux)",
			Value: "linux",
		},
		cli.BoolFlag{
			Name:  "readonly",
			Usage: "set the containers filesystem as readonly",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			err         error
			resp        *rootfsapi.MountResponse
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

		provider, err := getContentProvider(context)
		if err != nil {
			return err
		}

		rootfsClient, err := getRootFSService(context)
		if err != nil {
			return err
		}

		imageStore, err := getImageStore(context)
		if err != nil {
			return errors.Wrap(err, "failed resolving image store")
		}

		if runtime.GOOS != "windows" {
			ref := context.Args().First()

			image, err := imageStore.Get(ctx, ref)
			if err != nil {
				return errors.Wrapf(err, "could not resolve %q", ref)
			}
			image.Provider = provider
			// let's close out our db and tx so we don't hold the lock whilst running.

			diffIDs, err := image.RootFS(ctx)
			if err != nil {
				return err
			}

			if _, err := rootfsClient.Prepare(gocontext.TODO(), &rootfsapi.PrepareRequest{
				Name:    id,
				ChainID: identity.ChainID(diffIDs),
			}); err != nil {
				if grpc.Code(err) != codes.AlreadyExists {
					return err
				}
			}

			resp, err = rootfsClient.Mounts(gocontext.TODO(), &rootfsapi.MountsRequest{
				Name: id,
			})
			if err != nil {
				return err
			}

			ic, err := image.Config(ctx)
			if err != nil {
				return err
			}
			switch ic.MediaType {
			case ocispec.MediaTypeImageConfig, images.MediaTypeDockerSchema2Config:
				r, err := provider.Reader(ctx, ic.Digest)
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

		create, err := newCreateRequest(context, &imageConfig.Config, id, tmpDir)
		if err != nil {
			return err
		}
		if resp != nil {
			create.Rootfs = resp.Mounts
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

func handleConsoleResize(ctx gocontext.Context, service execution.ContainerServiceClient, id string, pid uint32, con console.Console) error {
	// do an initial resize of the console
	size, err := con.Size()
	if err != nil {
		return err
	}
	if _, err := service.Pty(ctx, &execution.PtyRequest{
		ID:     id,
		Pid:    pid,
		Width:  uint32(size.Width),
		Height: uint32(size.Height),
	}); err != nil {
		return err
	}
	s := make(chan os.Signal, 16)
	signal.Notify(s, unix.SIGWINCH)
	go func() {
		for range s {
			size, err := con.Size()
			if err != nil {
				logrus.WithError(err).Error("get pty size")
				continue
			}
			if _, err := service.Pty(ctx, &execution.PtyRequest{
				ID:     id,
				Pid:    pid,
				Width:  uint32(size.Width),
				Height: uint32(size.Height),
			}); err != nil {
				logrus.WithError(err).Error("resize pty")
			}
		}
	}()
	return nil
}
