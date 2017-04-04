package main

import (
	"context"
	"net"
	"path/filepath"
	"time"

	imagesapi "github.com/containerd/containerd/api/services/images"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	imagesservice "github.com/containerd/containerd/services/images"
	"github.com/urfave/cli"
	"google.golang.org/grpc"
)

func resolveContentStore(context *cli.Context) (*content.Store, error) {
	root := filepath.Join(context.GlobalString("root"), "content")
	if !filepath.IsAbs(root) {
		var err error
		root, err = filepath.Abs(root)
		if err != nil {
			return nil, err
		}
	}
	return content.NewStore(root)
}

func resolveImageStore(clicontext *cli.Context) (images.Store, error) {
	conn, err := connectGRPC(clicontext)
	if err != nil {
		return nil, err
	}
	return imagesservice.NewStoreFromClient(imagesapi.NewImagesClient(conn)), nil
}

func connectGRPC(context *cli.Context) (*grpc.ClientConn, error) {
	socket := context.GlobalString("socket")
	timeout := context.GlobalDuration("connect-timeout")
	return grpc.Dial(socket,
		grpc.WithTimeout(timeout),
		grpc.WithBlock(),
		grpc.WithInsecure(),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", socket, timeout)
		}),
	)
}

// getResolver prepares the resolver from the environment and options.
func getResolver(ctx context.Context) (remotes.Resolver, error) {
	return docker.NewResolver(), nil
}
