// +build linux

package linux

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"

	client "github.com/containerd/containerd/linux/shim"
)

func loadBundle(path, namespace string) *bundle {
	return &bundle{
		path:      path,
		namespace: namespace,
	}
}

// newBundle creates a new bundle on disk at the provided path for the given id
func newBundle(path, namespace, id string, spec []byte) (b *bundle, err error) {
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	path = filepath.Join(path, id)
	defer func() {
		if err != nil {
			os.RemoveAll(path)
		}
	}()
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(filepath.Join(path, "rootfs"), 0700); err != nil {
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
	}, err
}

type bundle struct {
	id        string
	path      string
	namespace string
}

// NewShim connects to the shim managing the bundle and tasks
func (b *bundle) NewShim(ctx context.Context, binary string, remote bool) (*client.Client, error) {
	opt := client.WithStart(binary)
	if !remote {
		opt = client.WithLocal
	}
	return client.New(ctx, client.Config{
		Address:   b.shimAddress(),
		Path:      b.path,
		Namespace: b.namespace,
	}, opt)
}

// Connect reconnects to an existing shim
func (b *bundle) Connect(ctx context.Context, remote bool) (*client.Client, error) {
	opt := client.WithConnect
	if !remote {
		opt = client.WithLocal
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
