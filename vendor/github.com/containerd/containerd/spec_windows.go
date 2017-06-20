package containerd

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/images"
	protobuf "github.com/gogo/protobuf/types"
	"github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const pipeRoot = `\\.\pipe`

func createDefaultSpec() (*specs.Spec, error) {
	return &specs.Spec{
		Version: specs.Version,
		Platform: specs.Platform{
			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		},
		Root: specs.Root{},
		Process: specs.Process{
			ConsoleSize: specs.Box{
				Width:  80,
				Height: 20,
			},
		},
	}, nil
}

func WithImageConfig(ctx context.Context, i Image) SpecOpts {
	return func(s *specs.Spec) error {
		var (
			image = i.(*image)
			store = image.client.ContentStore()
		)
		ic, err := image.i.Config(ctx, store)
		if err != nil {
			return err
		}
		var (
			ociimage v1.Image
			config   v1.ImageConfig
		)
		switch ic.MediaType {
		case v1.MediaTypeImageConfig, images.MediaTypeDockerSchema2Config:
			r, err := store.Reader(ctx, ic.Digest)
			if err != nil {
				return err
			}
			if err := json.NewDecoder(r).Decode(&ociimage); err != nil {
				r.Close()
				return err
			}
			r.Close()
			config = ociimage.Config
		default:
			return fmt.Errorf("unknown image config media type %s", ic.MediaType)
		}
		s.Process.Env = config.Env
		s.Process.Args = append(config.Entrypoint, config.Cmd...)
		s.Process.User = specs.User{
			Username: config.User,
		}
		return nil
	}
}

func WithTTY(width, height int) SpecOpts {
	return func(s *specs.Spec) error {
		s.Process.Terminal = true
		s.Process.ConsoleSize.Width = uint(width)
		s.Process.ConsoleSize.Height = uint(height)
		return nil
	}
}

func WithSpec(spec *specs.Spec) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		data, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		c.Spec = &protobuf.Any{
			TypeUrl: spec.Version,
			Value:   data,
		}
		return nil
	}
}
