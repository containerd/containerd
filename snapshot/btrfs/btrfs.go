package btrfs

import (
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/containerd"
	"github.com/docker/containerd/snapshot"
	"github.com/pkg/errors"
	"github.com/stevvooe/go-btrfs"
)

type Snapshotter struct {
	device string // maybe we can resolve it with path?
	root   string // root provides paths for internal storage.
}

func NewSnapshotter(device, root string) (*Snapshotter, error) {
	var (
		active    = filepath.Join(root, "active")
		snapshots = filepath.Join(root, "snapshots")
		parents   = filepath.Join(root, "parents")
		index     = filepath.Join(root, "index")
		names     = filepath.Join(root, "names")
	)

	for _, path := range []string{
		active,
		snapshots,
		parents,
		index,
		names,
	} {
		if err := os.MkdirAll(path, 0755); err != nil {
			return nil, err
		}
	}

	return &Snapshotter{device: device, root: root}, nil
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
func (b *Snapshotter) Stat(key string) (snapshot.Info, error) {
	// resolve the snapshot out of the index.
	target, err := os.Readlink(filepath.Join(b.root, "index", hash(key)))
	if err != nil {
		if !os.IsNotExist(err) {
			return snapshot.Info{}, err
		}

		return snapshot.Info{}, errors.Errorf("snapshot %v not found", key)
	}

	return b.stat(target)
}

func (b *Snapshotter) stat(target string) (snapshot.Info, error) {
	var (
		parents    = filepath.Join(b.root, "parents")
		names      = filepath.Join(b.root, "names")
		namep      = filepath.Join(names, filepath.Base(target))
		parentlink = filepath.Join(parents, filepath.Base(target))
	)

	// grab information about the subvolume
	info, err := btrfs.SubvolInfo(target)
	if err != nil {
		return snapshot.Info{}, err
	}

	// read the name out of the names!
	nameraw, err := ioutil.ReadFile(namep)
	if err != nil {
		return snapshot.Info{}, err
	}

	// resolve the parents path.
	parentp, err := os.Readlink(parentlink)
	if err != nil {
		if !os.IsNotExist(err) {
			return snapshot.Info{}, err
		}

		// no parent!
	}

	var parent string
	if parentp != "" {
		// okay, grab the basename of the parent and look up its name!
		parentnamep := filepath.Join(names, filepath.Base(parentp))

		p, err := ioutil.ReadFile(parentnamep)
		if err != nil {
			return snapshot.Info{}, err
		}

		parent = string(p)
	}

	kind := snapshot.KindCommitted
	if strings.HasPrefix(target, filepath.Join(b.root, "active")) {
		kind = snapshot.KindActive
	}

	return snapshot.Info{
		Name:     string(nameraw),
		Parent:   parent,
		Readonly: info.Readonly,
		Kind:     kind,
	}, nil

}

func (b *Snapshotter) Prepare(key, parent string) ([]containerd.Mount, error) {
	return b.makeActive(key, parent, false)
}

func (b *Snapshotter) View(key, parent string) ([]containerd.Mount, error) {
	return b.makeActive(key, parent, true)
}

func (b *Snapshotter) makeActive(key, parent string, readonly bool) ([]containerd.Mount, error) {
	var (
		active     = filepath.Join(b.root, "active")
		snapshots  = filepath.Join(b.root, "snapshots")
		parents    = filepath.Join(b.root, "parents")
		index      = filepath.Join(b.root, "index")
		names      = filepath.Join(b.root, "names")
		keyh       = hash(key)
		parenth    = hash(parent)
		target     = filepath.Join(active, keyh)
		namep      = filepath.Join(names, keyh)
		indexlink  = filepath.Join(index, keyh)
		parentlink = filepath.Join(parents, keyh)
		parentp    = filepath.Join(snapshots, parenth) // parent must be restricted to snaps
	)

	if parent == "" {
		// create new subvolume
		// btrfs subvolume create /dir
		if err := btrfs.SubvolCreate(target); err != nil {
			return nil, err
		}
	} else {
		// btrfs subvolume snapshot /parent /subvol
		if err := btrfs.SubvolSnapshot(target, parentp, readonly); err != nil {
			return nil, err
		}

		if err := os.Symlink(parentp, parentlink); err != nil {
			return nil, err
		}
	}

	// write in the name
	if err := ioutil.WriteFile(namep, []byte(key), 0644); err != nil {
		return nil, err
	}

	if err := os.Symlink(target, indexlink); err != nil {
		return nil, err
	}

	return b.mounts(target)
}

func (b *Snapshotter) mounts(dir string) ([]containerd.Mount, error) {
	var options []string

	// get the subvolume id back out for the mount
	info, err := btrfs.SubvolInfo(dir)
	if err != nil {
		return nil, err
	}

	options = append(options, fmt.Sprintf("subvolid=%d", info.ID))

	if info.Readonly {
		options = append(options, "ro")
	}

	return []containerd.Mount{
		{
			Type:   "btrfs",
			Source: b.device, // device?
			// NOTE(stevvooe): While it would be nice to use to uuids for
			// mounts, they don't work reliably if the uuids are missing.
			Options: options,
		},
	}, nil
}

func (b *Snapshotter) Commit(name, key string) error {
	var (
		active        = filepath.Join(b.root, "active")
		snapshots     = filepath.Join(b.root, "snapshots")
		index         = filepath.Join(b.root, "index")
		parents       = filepath.Join(b.root, "parents")
		names         = filepath.Join(b.root, "names")
		keyh          = hash(key)
		nameh         = hash(name)
		dir           = filepath.Join(active, keyh)
		target        = filepath.Join(snapshots, nameh)
		keynamep      = filepath.Join(names, keyh)
		namep         = filepath.Join(names, nameh)
		keyparentlink = filepath.Join(parents, keyh)
		parentlink    = filepath.Join(parents, nameh)
		keyindexlink  = filepath.Join(index, keyh)
		indexlink     = filepath.Join(index, nameh)
	)

	info, err := btrfs.SubvolInfo(dir)
	if err != nil {
		return err
	}

	if info.Readonly {
		return fmt.Errorf("may not commit view snapshot %q", dir)
	}

	// look up the parent information to make sure we have the right parent link.
	parentp, err := os.Readlink(keyparentlink)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}

		// we have no parent!
	}

	if err := os.MkdirAll(snapshots, 0755); err != nil {
		return err
	}

	if err := btrfs.SubvolSnapshot(target, dir, true); err != nil {
		return err
	}

	// remove the key name path as we no longer need it.
	if err := os.Remove(keynamep); err != nil {
		return err
	}

	if err := os.Remove(keyindexlink); err != nil {
		return err
	}

	if err := ioutil.WriteFile(namep, []byte(name), 0755); err != nil {
		return err
	}

	if parentp != "" {
		// move over the parent link into the commit name.
		if err := os.Rename(keyparentlink, parentlink); err != nil {
			return err
		}

		// TODO(stevvooe): For this to not break horribly, we should really
		// start taking a full lock. We are going to move all this metadata
		// into common storage, so let's not fret over it for now.
	}

	if err := os.Symlink(target, indexlink); err != nil {
		return err
	}

	if err := btrfs.SubvolDelete(dir); err != nil {
		return errors.Wrapf(err, "delete subvol failed on %v", dir)
	}

	return nil
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (b *Snapshotter) Mounts(key string) ([]containerd.Mount, error) {
	dir := filepath.Join(b.root, "active", hash(key))
	return b.mounts(dir)
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (b *Snapshotter) Remove(key string) error {
	panic("not implemented")
}

// Walk the committed snapshots.
func (b *Snapshotter) Walk(fn func(snapshot.Info) error) error {
	// TODO(stevvooe): Copy-pasted almost verbatim from overlay. Really need to
	// unify the metadata for snapshot implementations.
	root := filepath.Join(b.root, "index")
	return filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if path == root {
			return nil
		}

		if fi.Mode()&os.ModeSymlink == 0 {
			// only follow links
			return filepath.SkipDir
		}

		target, err := os.Readlink(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}

		si, err := b.stat(target)
		if err != nil {
			return err
		}

		if err := fn(si); err != nil {
			return err
		}

		return nil
	})
}

func hash(k string) string {
	return fmt.Sprintf("%x", sha256.Sum224([]byte(k)))
}
