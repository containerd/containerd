package main

import (
	"net"
	"path/filepath"
	"time"

	"github.com/boltdb/bolt"
	"github.com/docker/containerd/content"
	"github.com/docker/containerd/images"
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

func getDB(ctx *cli.Context, readonly bool) (*bolt.DB, error) {
	// TODO(stevvooe): For now, we operate directly on the database. We will
	// replace this with a GRPC service when the details are more concrete.
	path := filepath.Join(ctx.GlobalString("root"), "meta.db")

	db, err := bolt.Open(path, 0644, &bolt.Options{
		ReadOnly: readonly,
	})
	if err != nil {
		return nil, err
	}

	if !readonly {
		if err := images.InitDB(db); err != nil {
			return nil, err
		}
	}

	return db, nil
}
