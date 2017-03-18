package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	gocontext "context"

	"github.com/boltdb/bolt"
	contentapi "github.com/docker/containerd/api/services/content"
	"github.com/docker/containerd/api/services/execution"
	rootfsapi "github.com/docker/containerd/api/services/rootfs"
	"github.com/docker/containerd/api/types/container"
	"github.com/docker/containerd/content"
	"github.com/docker/containerd/image"
	contentservice "github.com/docker/containerd/services/content"
	"github.com/pkg/errors"
	"github.com/tonistiigi/fifo"
	"github.com/urfave/cli"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
)

var grpcConn *grpc.ClientConn

func prepareStdio(stdin, stdout, stderr string, console bool) (*sync.WaitGroup, error) {
	var wg sync.WaitGroup
	ctx := gocontext.Background()

	f, err := fifo.OpenFifo(ctx, stdin, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700)
	if err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)
	go func(w io.WriteCloser) {
		io.Copy(w, os.Stdin)
		w.Close()
	}(f)

	f, err = fifo.OpenFifo(ctx, stdout, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700)
	if err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)
	wg.Add(1)
	go func(r io.ReadCloser) {
		io.Copy(os.Stdout, r)
		r.Close()
		wg.Done()
	}(f)

	f, err = fifo.OpenFifo(ctx, stderr, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700)
	if err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)
	if !console {
		wg.Add(1)
		go func(r io.ReadCloser) {
			io.Copy(os.Stderr, r)
			r.Close()
			wg.Done()
		}(f)
	}

	return &wg, nil
}

func getGRPCConnection(context *cli.Context) (*grpc.ClientConn, error) {
	if grpcConn != nil {
		return grpcConn, nil
	}

	bindSocket := context.GlobalString("socket")
	// reset the logger for grpc to log to dev/null so that it does not mess with our stdio
	grpclog.SetLogger(log.New(ioutil.Discard, "", log.LstdFlags))
	dialOpts := []grpc.DialOption{grpc.WithInsecure(), grpc.WithTimeout(100 * time.Second)}
	dialOpts = append(dialOpts,
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", bindSocket, timeout)
		},
		))

	conn, err := grpc.Dial(fmt.Sprintf("unix://%s", bindSocket), dialOpts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial %q", bindSocket)
	}

	grpcConn = conn
	return grpcConn, nil
}

func getExecutionService(context *cli.Context) (execution.ContainerServiceClient, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return execution.NewContainerServiceClient(conn), nil
}

func getContentProvider(context *cli.Context) (content.Provider, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return contentservice.NewProviderFromClient(contentapi.NewContentClient(conn)), nil
}

func getRootFSService(context *cli.Context) (rootfsapi.RootFSClient, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return rootfsapi.NewRootFSClient(conn), nil
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
		if err := image.InitDB(db); err != nil {
			return nil, err
		}
	}

	return db, nil
}

func getTempDir(id string) (string, error) {
	err := os.MkdirAll(filepath.Join(os.TempDir(), "ctr"), 0700)
	if err != nil {
		return "", err
	}
	tmpDir, err := ioutil.TempDir(filepath.Join(os.TempDir(), "ctr"), fmt.Sprintf("%s-", id))
	if err != nil {
		return "", err
	}
	return tmpDir, nil
}

func waitContainer(events execution.ContainerService_EventsClient, response *execution.CreateResponse) (uint32, error) {
	for {
		e, err := events.Recv()
		if err != nil {
			return 255, err
		}
		if e.Type != container.Event_EXIT {
			continue
		}
		if e.ID == response.ID &&
			e.Pid == response.Pid {
			return e.ExitStatus, nil
		}
	}
}
