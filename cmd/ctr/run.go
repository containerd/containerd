package main

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/console"
	containersapi "github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshot"
	digest "github.com/opencontainers/go-digest"
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
		cli.StringSliceFlag{
			Name:  "mount",
			Usage: "specify additional container mount (ex: type=bind,src=/tmp,dest=/host,options=rbind:ro)",
		},
		cli.BoolFlag{
			Name:  "rm",
			Usage: "remove the container after running",
		},
		cli.StringFlag{
			Name:  "checkpoint",
			Usage: "provide the checkpoint digest to restore the container",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			err         error
			mounts      []mount.Mount
			imageConfig ocispec.Image

			ctx = gocontext.Background()
			id  = context.String("id")
		)
		if id == "" {
			return errors.New("container id must be provided")
		}
		containers, err := getContainersService(context)
		if err != nil {
			return err
		}
		tasks, err := getTasksService(context)
		if err != nil {
			return err
		}
		tmpDir, err := getTempDir(id)
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		events, err := tasks.Events(ctx, &execution.EventsRequest{})
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
		differ, err := getDiffService(context)
		if err != nil {
			return err
		}
		var (
			checkpoint      *ocispec.Descriptor
			checkpointIndex digest.Digest
			ref             = context.Args().First()
		)
		if raw := context.String("checkpoint"); raw != "" {
			if checkpointIndex, err = digest.Parse(raw); err != nil {
				return err
			}
		}
		var spec []byte
		if checkpointIndex != "" {
			var index ocispec.ImageIndex
			r, err := content.Reader(ctx, checkpointIndex)
			if err != nil {
				return err
			}
			err = json.NewDecoder(r).Decode(&index)
			r.Close()
			if err != nil {
				return err
			}
			var rw ocispec.Descriptor
			for _, m := range index.Manifests {
				switch m.MediaType {
				case images.MediaTypeContainerd1Checkpoint:
					fkingo := m.Descriptor
					checkpoint = &fkingo
				case images.MediaTypeContainerd1CheckpointConfig:
					if r, err = content.Reader(ctx, m.Digest); err != nil {
						return err
					}
					spec, err = ioutil.ReadAll(r)
					r.Close()
					if err != nil {
						return err
					}
				case images.MediaTypeDockerSchema2Manifest:
					// make sure we have the original image that was used during checkpoint
					diffIDs, err := images.RootFS(ctx, content, m.Descriptor)
					if err != nil {
						return err
					}
					if _, err := snapshotter.Prepare(ctx, id, identity.ChainID(diffIDs).String()); err != nil {
						if !snapshot.IsExist(err) {
							return err
						}
					}
				case ocispec.MediaTypeImageLayer:
					rw = m.Descriptor
				}
			}
			if mounts, err = snapshotter.Mounts(ctx, id); err != nil {
				return err
			}
			if _, err := differ.Apply(ctx, rw, mounts); err != nil {
				return err
			}
		} else {
			if runtime.GOOS != "windows" && context.String("rootfs") == "" {
				image, err := imageStore.Get(ctx, ref)
				if err != nil {
					return errors.Wrapf(err, "could not resolve %q", ref)
				}
				// let's close out our db and tx so we don't hold the lock whilst running.
				diffIDs, err := image.RootFS(ctx, content)
				if err != nil {
					return err
				}
				if context.Bool("readonly") {
					mounts, err = snapshotter.View(ctx, id, identity.ChainID(diffIDs).String())
				} else {
					mounts, err = snapshotter.Prepare(ctx, id, identity.ChainID(diffIDs).String())
				}
				defer func() {
					if err != nil || context.Bool("rm") {
						if err := snapshotter.Remove(ctx, id); err != nil {
							logrus.WithError(err).Errorf("failed to remove snapshot %q", id)
						}
					}
				}()
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
		}
		if err != nil {
			return err
		}
		if len(spec) == 0 {
			if spec, err = newContainerSpec(context, &imageConfig.Config, ref); err != nil {
				return err
			}
		}

		createContainer, err := newCreateContainerRequest(context, id, id, spec)
		if err != nil {
			return err
		}

		_, err = containers.Create(ctx, createContainer)
		if err != nil {
			return err
		}

		create, err := newCreateTaskRequest(context, id, tmpDir, checkpoint, mounts)
		if err != nil {
			return err
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
		response, err := tasks.Create(ctx, create)
		if err != nil {
			return err
		}
		pid := response.Pid
		if create.Terminal {
			if err := handleConsoleResize(ctx, tasks, id, pid, con); err != nil {
				logrus.WithError(err).Error("console resize")
			}
		} else {
			sigc := forwardAllSignals(tasks, id)
			defer stopCatch(sigc)
		}
		if checkpoint == nil {
			if _, err := tasks.Start(ctx, &execution.StartRequest{
				ContainerID: id,
			}); err != nil {
				return err
			}
		}
		// Ensure we read all io only if container started successfully.
		defer fwg.Wait()

		status, err := waitContainer(events, id, pid)
		if err != nil {
			return err
		}
		if _, err := tasks.Delete(ctx, &execution.DeleteRequest{
			ContainerID: response.ContainerID,
		}); err != nil {
			return err
		}
		if context.Bool("rm") {
			if _, err := containers.Delete(ctx, &containersapi.DeleteContainerRequest{ID: id}); err != nil {
				return err
			}
		}
		if status != 0 {
			return cli.NewExitError("", int(status))
		}
		return nil
	},
}
