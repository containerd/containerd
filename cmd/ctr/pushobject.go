package main

import (
	"fmt"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli"
)

var pushObjectCommand = cli.Command{
	Name:        "push-object",
	Usage:       "push an object to a remote",
	ArgsUsage:   "[flags] <remote> <object> <type>",
	Description: `Push objects by identifier to a remote.`,
	Flags:       registryFlags,
	Action: func(clicontext *cli.Context) error {
		var (
			ref    = clicontext.Args().Get(0)
			object = clicontext.Args().Get(1)
			media  = clicontext.Args().Get(2)
		)
		dgst, err := digest.Parse(object)
		if err != nil {
			return err
		}

		ctx, cancel := appContext(clicontext)
		defer cancel()

		resolver, err := getResolver(ctx, clicontext)
		if err != nil {
			return err
		}

		ctx = log.WithLogger(ctx, log.G(ctx).WithField("ref", ref))

		log.G(ctx).Infof("resolving")
		pusher, err := resolver.Pusher(ctx, ref)
		if err != nil {
			return err
		}

		cs, err := getContentStore(clicontext)
		if err != nil {
			return err
		}

		info, err := cs.Info(ctx, dgst)
		if err != nil {
			return err
		}
		desc := ocispec.Descriptor{
			MediaType: media,
			Digest:    dgst,
			Size:      info.Size,
		}

		ra, err := cs.ReaderAt(ctx, dgst)
		if err != nil {
			return err
		}
		defer ra.Close()

		cw, err := pusher.Push(ctx, desc)
		if err != nil {
			return err
		}

		// TODO: Progress reader
		if err := content.Copy(ctx, cw, content.NewReader(ra), desc.Size, desc.Digest); err != nil {
			return err
		}

		fmt.Printf("Pushed %s %s\n", desc.Digest, desc.MediaType)

		return nil
	},
}
