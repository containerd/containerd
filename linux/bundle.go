// +build linux

package linux

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/linux/runcopts"
	client "github.com/containerd/containerd/linux/shim"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/typeurl"
)

func loadBundle(path, namespace string, events *events.Exchange) *bundle {
	return &bundle{
		path:      path,
		namespace: namespace,
		events:    events,
	}
}

// newBundle creates a new bundle on disk at the provided path for the given id
func newBundle(path, namespace, id string, spec []byte, events *events.Exchange) (b *bundle, err error) {
	if err := os.MkdirAll(path, 0711); err != nil {
		return nil, err
	}
	path = filepath.Join(path, id)
	defer func() {
		if err != nil {
			os.RemoveAll(path)
		}
	}()
	if err := os.Mkdir(path, 0711); err != nil {
		return nil, err
	}
	if err := os.Mkdir(filepath.Join(path, "rootfs"), 0711); err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(path, configFilename))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_, err = io.Copy(f, bytes.NewReader(spec))
	return &bundle{
		id:        id,
		path:      path,
		namespace: namespace,
		events:    events,
	}, err
}

type bundle struct {
	id        string
	path      string
	namespace string
	events    *events.Exchange
}

// NewShim connects to the shim managing the bundle and tasks
func (b *bundle) NewShim(ctx context.Context, binary, grpcAddress string, remote, debug bool, createOpts runtime.CreateOpts) (*client.Client, error) {
	opt := client.WithStart(binary, grpcAddress, debug)
	if !remote {
		opt = client.WithLocal(b.events)
	}
	var options runcopts.CreateOptions
	if createOpts.Options != nil {
		v, err := typeurl.UnmarshalAny(createOpts.Options)
		if err != nil {
			return nil, err
		}
		options = *v.(*runcopts.CreateOptions)
	}
	return client.New(ctx, client.Config{
		Address:    b.shimAddress(),
		Path:       b.path,
		Namespace:  b.namespace,
		CgroupPath: options.ShimCgroup,
	}, opt)
}

// Connect reconnects to an existing shim
func (b *bundle) Connect(ctx context.Context, remote bool) (*client.Client, error) {
	opt := client.WithConnect
	if !remote {
		opt = client.WithLocal(b.events)
	}
	return client.New(ctx, client.Config{
		Address:   b.shimAddress(),
		Path:      b.path,
		Namespace: b.namespace,
	}, opt)
}

// Delete deletes the bundle from disk
func (b *bundle) Delete() error {
	return os.RemoveAll(b.path)
}

func (b *bundle) shimAddress() string {
	return filepath.Join(string(filepath.Separator), "containerd-shim", b.namespace, b.id, "shim.sock")

}
