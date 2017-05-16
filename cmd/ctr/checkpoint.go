package main

import (
	"bytes"
	gocontext "context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/descriptor"
	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/rootfs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var checkpointCommand = cli.Command{
	Name:  "checkpoint",
	Usage: "checkpoint a container",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "id",
			Usage: "id of the container",
		},
		cli.BoolFlag{
			Name:  "exit",
			Usage: "stop the container after the checkpoint",
		},
		cli.BoolFlag{
			Name:  "binds",
			Usage: "checkpoint bind mounts with the checkpoint",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			id  = context.String("id")
			ctx = gocontext.Background()
		)
		if id == "" {
			return errors.New("container id must be provided")
		}

		tasks, err := getTasksService(context)
		if err != nil {
			return err
		}
		content, err := getContentStore(context)
		if err != nil {
			return err
		}
		imageStore, err := getImageStore(context)
		if err != nil {
			return errors.Wrap(err, "failed resolving image store")
		}
		var spec specs.Spec
		info, err := tasks.Info(ctx, &execution.InfoRequest{
			ContainerID: id,
		})
		if err != nil {
			return err
		}
		if err := json.Unmarshal(info.Task.Spec.Value, &spec); err != nil {
			return err
		}
		stopped := context.Bool("exit")
		// if the container will still be running after the checkpoint make sure that
		// we pause the container and give us time to checkpoint the filesystem before
		// it resumes execution
		if !stopped {
			if _, err := tasks.Pause(ctx, &execution.PauseRequest{
				ContainerID: id,
			}); err != nil {
				return err
			}
			defer func() {
				if _, err := tasks.Resume(ctx, &execution.ResumeRequest{
					ContainerID: id,
				}); err != nil {
					logrus.WithError(err).Error("ctr: unable to resume container")
				}
			}()
		}
		checkpoint, err := tasks.Checkpoint(ctx, &execution.CheckpointRequest{
			ContainerID: id,
			Exit:        context.Bool("exit"),
		})
		if err != nil {
			return err
		}
		image, err := imageStore.Get(ctx, spec.Annotations["image"])
		if err != nil {
			return err
		}
		var additionalDescriptors []*descriptor.Descriptor
		if context.Bool("binds") {
			if additionalDescriptors, err = checkpointBinds(ctx, &spec, content); err != nil {
				return err
			}
		}
		var index ocispec.ImageIndex
		for _, d := range append(checkpoint.Descriptors, additionalDescriptors...) {
			index.Manifests = append(index.Manifests, ocispec.ManifestDescriptor{
				Descriptor: ocispec.Descriptor{
					MediaType: d.MediaType,
					Size:      d.Size_,
					Digest:    d.Digest,
				},
				Platform: ocispec.Platform{
					OS:           runtime.GOOS,
					Architecture: runtime.GOARCH,
				},
			})
		}
		// add image to the index
		index.Manifests = append(index.Manifests, ocispec.ManifestDescriptor{
			Descriptor: image.Target,
		})
		// checkpoint rw layer
		snapshotter, err := getSnapshotter(context)
		if err != nil {
			return err
		}
		differ, err := getDiffService(context)
		if err != nil {
			return err
		}
		rw, err := rootfs.Diff(ctx, id, fmt.Sprintf("checkpoint-rw-%s", id), snapshotter, differ)
		if err != nil {
			return err
		}
		index.Manifests = append(index.Manifests, ocispec.ManifestDescriptor{
			Descriptor: rw,
			Platform: ocispec.Platform{
				OS:           runtime.GOOS,
				Architecture: runtime.GOARCH,
			},
		})
		data, err := json.Marshal(index)
		if err != nil {
			return err
		}
		// write the index to the content store
		buf := bytes.NewReader(data)
		desc, err := writeContent(ctx, content, ocispec.MediaTypeImageIndex, id, buf)
		if err != nil {
			return err
		}
		fmt.Println(desc.Digest.String())
		return nil
	},
}

func checkpointBinds(ctx gocontext.Context, s *specs.Spec, store content.Store) ([]*descriptor.Descriptor, error) {
	var out []*descriptor.Descriptor
	for _, m := range s.Mounts {
		if m.Type != "bind" {
			continue
		}
		tar := archive.Diff(ctx, "", m.Source)
		d, err := writeContent(ctx, store, images.MediaTypeContainerd1Resource, m.Source, tar)
		if err := tar.Close(); err != nil {
			return nil, err
		}
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func writeContent(ctx gocontext.Context, store content.Store, mediaType, ref string, r io.Reader) (*descriptor.Descriptor, error) {
	writer, err := store.Writer(ctx, ref, 0, "")
	if err != nil {
		return nil, err
	}
	defer writer.Close()
	size, err := io.Copy(writer, r)
	if err != nil {
		return nil, err
	}
	if err := writer.Commit(0, ""); err != nil {
		return nil, err
	}
	return &descriptor.Descriptor{
		MediaType: mediaType,
		Digest:    writer.Digest(),
		Size_:     size,
	}, nil
}
