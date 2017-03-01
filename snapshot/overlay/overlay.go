package overlay

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/containerd"
	"github.com/docker/containerd/snapshot"
	"github.com/docker/containerd/snapshot/storage"
	"github.com/pkg/errors"
)

type Snapshotter struct {
	root string
	ms   storage.MetaStore
}

type activeSnapshot struct {
	id       string
	name     string
	parentID interface{}
	readonly bool
}

func NewSnapshotter(root string, ms storage.MetaStore) (*Snapshotter, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Join(root, "snapshots"), 0700); err != nil {
		return nil, err
	}

	return &Snapshotter{
		root: root,
		ms:   ms,
	}, nil
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
func (o *Snapshotter) Stat(ctx context.Context, key string) (snapshot.Info, error) {
	return o.ms.Stat(ctx, key)
}

func (o *Snapshotter) Prepare(ctx context.Context, key, parent string) ([]containerd.Mount, error) {
	return o.createActive(ctx, key, parent, false)
}

func (o *Snapshotter) View(ctx context.Context, key, parent string) ([]containerd.Mount, error) {
	return o.createActive(ctx, key, parent, true)
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (o *Snapshotter) Mounts(ctx context.Context, key string) ([]containerd.Mount, error) {
	active, err := o.ms.GetActive(ctx, key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get active mount")
	}
	return o.mounts(active), nil
}

func (o *Snapshotter) Commit(ctx context.Context, name, key string) error {
	return o.ms.Commit(ctx, name, key)
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (o *Snapshotter) Remove(ctx context.Context, key string) error {
	var (
		path, renamed string
	)
	err := o.ms.Remove(ctx, key, func(id string) error {
		path = filepath.Join(o.root, "snapshots", id)
		renamed = filepath.Join(o.root, "snapshots", "rm-"+id)
		if err := os.Rename(path, renamed); err != nil {
			path = ""
			renamed = ""
			return errors.Wrap(err, "failed to rename")
		}
		return nil
	})
	if err != nil {
		err = errors.Wrap(err, "failed to perform remove")
		if path != "" && renamed != "" {
			if err1 := os.Rename(renamed, path); err1 != nil {
				err = errors.Wrapf(err, "clean up rename failure: %v", err1)
			}
		}
		return err
	}
	if err := os.RemoveAll(renamed); err != nil {
		// TODO: just log and cleanup later
		return errors.Wrapf(err, "failed to remove root: %v", renamed)
	}

	return nil
}

// Walk the committed snapshots.
func (o *Snapshotter) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	return o.ms.Walk(ctx, fn)
}

func (o *Snapshotter) createActive(ctx context.Context, key, parent string, readonly bool) ([]containerd.Mount, error) {
	var (
		path        string
		snapshotDir = filepath.Join(o.root, "snapshots")
	)

	td, err := ioutil.TempDir(snapshotDir, "new-")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create temp dir")
	}
	defer func() {
		if err != nil {
			if td != "" {
				if err1 := os.RemoveAll(td); err1 != nil {
					err = errors.Wrapf(err, "remove failed: %v", err1)
				}
			}
			if path != "" {
				if err1 := os.RemoveAll(path); err1 != nil {
					err = errors.Wrapf(err, "failed to remove path: %v", err1)
				}
			}
		}
	}()

	if err = os.MkdirAll(filepath.Join(td, "fs"), 0700); err != nil {
		return nil, err
	}
	if !readonly {
		if err = os.MkdirAll(filepath.Join(td, "work"), 0700); err != nil {
			return nil, err
		}
	}
	opts := storage.CreateActiveOpts{
		Parent:   parent,
		Readonly: readonly,
		Create: func(id string) error {
			path = filepath.Join(snapshotDir, id)
			if err := os.Rename(td, path); err != nil {
				return errors.Wrap(err, "failed to rename")
			}
			td = ""

			return nil
		},
	}
	active, err := o.ms.CreateActive(ctx, key, opts)
	if err != nil {
		return nil, err
	}

	return o.mounts(active), nil
}

func (o *Snapshotter) mounts(active storage.Active) []containerd.Mount {
	if len(active.ParentIDs) == 0 {
		// if we only have one layer/no parents then just return a bind mount as overlay
		// will not work
		roFlag := "rw"
		if active.Readonly {
			roFlag = "ro"
		}

		return []containerd.Mount{
			{
				Source: o.upperPath(active.ID),
				Type:   "bind",
				Options: []string{
					roFlag,
					"rbind",
				},
			},
		}
	}
	var options []string

	if !active.Readonly {
		options = append(options,
			fmt.Sprintf("workdir=%s", o.workPath(active.ID)),
			fmt.Sprintf("upperdir=%s", o.upperPath(active.ID)),
		)
	} else if len(active.ParentIDs) == 1 {
		return []containerd.Mount{
			{
				Source: o.upperPath(active.ParentIDs[0]),
				Type:   "bind",
				Options: []string{
					"ro",
					"rbind",
				},
			},
		}
	}

	parentPaths := make([]string, len(active.ParentIDs))
	for i := range active.ParentIDs {
		parentPaths[i] = o.upperPath(active.ParentIDs[i])
	}

	options = append(options, fmt.Sprintf("lowerdir=%s", strings.Join(parentPaths, ":")))
	return []containerd.Mount{
		{
			Type:    "overlay",
			Source:  "overlay",
			Options: options,
		},
	}

}

func (o *Snapshotter) upperPath(id string) string {
	return filepath.Join(o.root, "snapshots", id, "fs")
}

func (o *Snapshotter) workPath(id string) string {
	return filepath.Join(o.root, "snapshots", id, "work")
}
