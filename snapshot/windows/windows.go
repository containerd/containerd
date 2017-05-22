// +build windows

package windows

import (
	"context"
	"path/filepath"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshot"
	"github.com/pkg/errors"
)

var (
	ErrNotImplemented = errors.New("not implemented")
)

func init() {
	plugin.Register("snapshot-windows", &plugin.Registration{
		Type: plugin.SnapshotPlugin,
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			return NewSnapshotter(filepath.Join(ic.Root, "snapshot", "windows"))
		},
	})
}

type Snapshotter struct {
	root string
}

func NewSnapshotter(root string) (snapshot.Snapshotter, error) {
	return &Snapshotter{
		root: root,
	}, nil
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
func (o *Snapshotter) Stat(ctx context.Context, key string) (snapshot.Info, error) {
	panic("not implemented")
}

func (o *Snapshotter) Usage(ctx context.Context, key string) (snapshot.Usage, error) {
	panic("not implemented")
}

func (o *Snapshotter) Prepare(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	panic("not implemented")
}

func (o *Snapshotter) View(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	panic("not implemented")
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (o *Snapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	panic("not implemented")
}

func (o *Snapshotter) Commit(ctx context.Context, name, key string) error {
	panic("not implemented")
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (o *Snapshotter) Remove(ctx context.Context, key string) error {
	panic("not implemented")
}

// Walk the committed snapshots.
func (o *Snapshotter) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	panic("not implemented")
}
