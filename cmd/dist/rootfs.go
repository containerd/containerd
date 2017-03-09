package main

import (
	"context"
	"encoding/json"
	"io/ioutil"

	contentapi "github.com/docker/containerd/api/services/content"
	rootfsapi "github.com/docker/containerd/api/services/rootfs"
	"github.com/docker/containerd/content"
	"github.com/docker/containerd/log"
	contentservice "github.com/docker/containerd/services/content"
	rootfsservice "github.com/docker/containerd/services/rootfs"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli"
)

var rootfsCommand = cli.Command{
	Name:  "rootfs",
	Usage: "rootfs setups a rootfs",
	Subcommands: []cli.Command{
		rootfsPrepareCommand,
	},
}

var rootfsPrepareCommand = cli.Command{
	Name:      "prepare",
	Usage:     "prepare applies layers from a manifest to a snapshot",
	ArgsUsage: "[flags] <digest>",
	Flags:     []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		var (
			ctx = background
		)

		dgst, err := digest.Parse(clicontext.Args().First())
		if err != nil {
			return err
		}

		log.G(ctx).Infof("preparing manifest %s", dgst.String())

		conn, err := connectGRPC(clicontext)
		if err != nil {
			return err
		}

		provider := contentservice.NewProviderFromClient(contentapi.NewContentClient(conn))
		m, err := resolveManifest(ctx, provider, dgst)
		if err != nil {
			return err
		}

		preparer := rootfsservice.NewPreparerFromClient(rootfsapi.NewRootFSClient(conn))
		chainID, err := preparer.Prepare(ctx, m.Layers)
		if err != nil {
			return err
		}

		log.G(ctx).Infof("chain ID: %s", chainID.String())

		return nil
	},
}

func resolveManifest(ctx context.Context, provider content.Provider, dgst digest.Digest) (ocispec.Manifest, error) {
	p, err := readAll(ctx, provider, dgst)
	if err != nil {
		return ocispec.Manifest{}, err
	}

	// TODO(stevvooe): This assumption that we get a manifest is unfortunate.
	// Need to provide way to resolve what the type of the target is.
	var manifest ocispec.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return ocispec.Manifest{}, err
	}

	return manifest, nil
}

func readAll(ctx context.Context, provider content.Provider, dgst digest.Digest) ([]byte, error) {
	rc, err := provider.Reader(ctx, dgst)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	return ioutil.ReadAll(rc)
}
