// +build windows

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package runtime

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/runtime/shim"
	"github.com/containerd/containerd/runtime/shim/client"
	"github.com/pkg/errors"
)

// loadBundle loads an existing bundle from disk
func loadBundle(id, path string) *bundle {
	return &bundle{
		id:   id,
		path: path,
	}
}

// newBundle creates a new bundle on disk at the provided path for the given id
func newBundle(id, path string, spec []byte) (b *bundle, err error) {
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
	err = ioutil.WriteFile(filepath.Join(path, configFilename), spec, 0666)
	return &bundle{
		id:   id,
		path: path,
	}, err
}

// bundle in Windows has a slightly different concept. It will contain no rootfs
// or mount points but acts primarily as the destination for the config.json OCI
// spec passed to runhcs.
type bundle struct {
	id   string
	path string
}

// ShimOpt specifies shim options for initialization and connection
type ShimOpt func(*bundle, string) (shim.Config, client.Opt)

// ShimRemote is a ShimOpt for connecting and starting a remote shim
func ShimRemote(c *Config, daemonAddress string, exitHandler func()) ShimOpt {
	return func(b *bundle, ns string) (shim.Config, client.Opt) {
		config := b.shimConfig(ns, c)
		return config,
			client.WithStart(c.Shim, b.shimAddress(ns), daemonAddress, c.ShimDebug, exitHandler)
	}
}

// ShimLocal is a ShimOpt for using an in process shim implementation
func ShimLocal(c *Config, exchange *exchange.Exchange) ShimOpt {
	return func(b *bundle, ns string) (shim.Config, client.Opt) {
		return b.shimConfig(ns, c), client.WithLocal(exchange)
	}
}

// ShimConnect is a ShimOpt for connecting to an existing remote shim
func ShimConnect(c *Config, onClose func()) ShimOpt {
	return func(b *bundle, ns string) (shim.Config, client.Opt) {
		return b.shimConfig(ns, c), client.WithConnect(b.shimAddress(ns), onClose)
	}
}

// NewShimClient connects to the shim managing the bundle and tasks creating it if needed
func (b *bundle) NewShimClient(ctx context.Context, namespace string, getClientOpts ShimOpt) (*client.Client, error) {
	cfg, opt := getClientOpts(b, namespace)
	return client.New(ctx, cfg, opt)
}

// Delete deletes the bundle from disk
func (b *bundle) Delete() error {
	err := os.RemoveAll(b.path)
	if err != nil {
		return errors.Wrapf(err, "Failed to remove bundle path: %s", b.path)
	}
	return nil
}

func (b *bundle) shimAddress(namespace string) string {
	return filepath.Join(string(filepath.Separator), "containerd-shim", namespace, b.id, "shim.sock")
}

func (b *bundle) shimConfig(namespace string, c *Config) shim.Config {
	return shim.Config{
		Path:        b.path,
		Namespace:   namespace,
		RuntimeRoot: c.RuntimeRoot,
	}
}
