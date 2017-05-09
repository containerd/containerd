package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	rootfsapi "github.com/containerd/containerd/api/services/rootfs"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/log"
	rootfsservice "github.com/containerd/containerd/services/rootfs"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli"
)

var rootfsCommand = cli.Command{
	Name:  "rootfs",
	Usage: "rootfs setups a rootfs",
	Subcommands: []cli.Command{
		rootfsUnpackCommand,
		rootfsPrepareCommand,
	},
}

var rootfsUnpackCommand = cli.Command{
	Name:      "unpack",
	Usage:     "unpack applies layers from a manifest to a snapshot",
	ArgsUsage: "[flags] <digest>",
	Flags:     []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext()
		defer cancel()

		dgst, err := digest.Parse(clicontext.Args().First())
		if err != nil {
			return err
		}

		log.G(ctx).Infof("unpacking layers from manifest %s", dgst.String())

		conn, err := connectGRPC(clicontext)
		if err != nil {
			return err
		}

		cs, err := resolveContentStore(clicontext)
		if err != nil {
			return err
		}

		m, err := resolveManifest(ctx, cs, dgst)
		if err != nil {
			return err
		}

		unpacker := rootfsservice.NewUnpackerFromClient(rootfsapi.NewRootFSClient(conn))
		chainID, err := unpacker.Unpack(ctx, m.Layers)
		if err != nil {
			return err
		}

		log.G(ctx).Infof("chain ID: %s", chainID.String())

		return nil
	},
}

var rootfsPrepareCommand = cli.Command{
	Name:      "prepare",
	Usage:     "prepare gets mount commands for digest",
	ArgsUsage: "[flags] <digest> <target>",
	Flags:     []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext()
		defer cancel()

		if clicontext.NArg() != 2 {
			return cli.ShowSubcommandHelp(clicontext)
		}

		dgst, err := digest.Parse(clicontext.Args().Get(0))
		if err != nil {
			return err
		}
		target := clicontext.Args().Get(1)

		log.G(ctx).Infof("preparing mounts %s", dgst.String())

		conn, err := connectGRPC(clicontext)
		if err != nil {
			return err
		}

		rclient := rootfsapi.NewRootFSClient(conn)

		ir := &rootfsapi.PrepareRequest{
			Name:    target,
			ChainID: dgst,
		}

		resp, err := rclient.Prepare(ctx, ir)
		if err != nil {
			return err
		}

		for _, m := range resp.Mounts {
			fmt.Fprintf(os.Stdout, "mount -t %s %s %s -o %s\n", m.Type, m.Source, target, strings.Join(m.Options, ","))
		}

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
