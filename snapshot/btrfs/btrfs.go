package btrfs

import (
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/docker/containerd"
	"github.com/docker/containerd/log"
	"github.com/pkg/errors"
	"github.com/stevvooe/go-btrfs"
)

type Driver struct {
	device string // maybe we can resolve it with path?
	root   string // root provides paths for internal storage.
}

func NewDriver(device, root string) (*Driver, error) {
	return &Driver{device: device, root: root}, nil
}

func (b *Driver) Prepare(key, parent string) ([]containerd.Mount, error) {
	return b.makeActive(key, parent, false)
}

func (b *Driver) View(key, parent string) ([]containerd.Mount, error) {
	return b.makeActive(key, parent, true)
}

func (b *Driver) makeActive(key, parent string, readonly bool) ([]containerd.Mount, error) {
	var (
		active     = filepath.Join(b.root, "active")
		parents    = filepath.Join(b.root, "parents")
		snapshots  = filepath.Join(b.root, "snapshots")
		names      = filepath.Join(b.root, "names")
		keyh       = hash(key)
		parenth    = hash(parent)
		dir        = filepath.Join(active, keyh)
		namep      = filepath.Join(names, keyh)
		parentlink = filepath.Join(parents, keyh)
		parentp    = filepath.Join(snapshots, parenth)
	)

	for _, path := range []string{
		active,
		parents,
		names,
	} {
		if err := os.MkdirAll(path, 0755); err != nil {
			return nil, err
		}
	}

	if parent == "" {
		// create new subvolume
		// btrfs subvolume create /dir
		if err := btrfs.SubvolCreate(dir); err != nil {
			return nil, err
		}
	} else {
		// btrfs subvolume snapshot /parent /subvol
		if err := btrfs.SubvolSnapshot(dir, parentp, readonly); err != nil {
			return nil, err
		}

		if err := os.Symlink(parentp, parentlink); err != nil {
			return nil, err
		}
	}

	if err := ioutil.WriteFile(namep, []byte(key), 0644); err != nil {
		return nil, err
	}

	return b.mounts(dir)
}

func (b *Driver) mounts(dir string) ([]containerd.Mount, error) {
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

func (b *Driver) Commit(name, key string) error {
	var (
		active        = filepath.Join(b.root, "active")
		snapshots     = filepath.Join(b.root, "snapshots")
		names         = filepath.Join(b.root, "names")
		parents       = filepath.Join(b.root, "parents")
		keyh          = hash(key)
		nameh         = hash(name)
		dir           = filepath.Join(active, keyh)
		target        = filepath.Join(snapshots, nameh)
		keynamep      = filepath.Join(names, keyh)
		namep         = filepath.Join(names, nameh)
		keyparentlink = filepath.Join(parents, keyh)
		parentlink    = filepath.Join(parents, nameh)
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

	fmt.Println("commit snapshot", target)
	if err := btrfs.SubvolSnapshot(target, dir, true); err != nil {
		fmt.Println("snapshot error")
		return err
	}

	// remove the key name path as we no longer need it.
	if err := os.Remove(keynamep); err != nil {
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

	if err := btrfs.SubvolDelete(dir); err != nil {
		return errors.Wrapf(err, "delete subvol failed on %v", dir)
	}

	return nil
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (b *Driver) Mounts(key string) ([]containerd.Mount, error) {
	dir := filepath.Join(b.root, "active", hash(key))
	return b.mounts(dir)
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (b *Driver) Remove(key string) error {
	panic("not implemented")
}

// Parent returns the parent of snapshot identified by name.
func (b *Driver) Parent(name string) (string, error) {
	var (
		parents    = filepath.Join(b.root, "parents")
		names      = filepath.Join(b.root, "names")
		parentlink = filepath.Join(parents, hash(name))
	)

	parentp, err := os.Readlink(parentlink)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}

		return "", nil // no parent!
	}

	// okay, grab the basename of the parent and look up its name!
	parentnamep := filepath.Join(names, filepath.Base(parentp))

	p, err := ioutil.ReadFile(parentnamep)
	if err != nil {
		return "", err
	}

	return string(p), nil
}

// Exists returns true if the snapshot with name exists.
func (b *Driver) Exists(name string) bool {
	target := filepath.Join(b.root, "snapshots", hash(name))

	if _, err := os.Stat(target); err != nil {
		if !os.IsNotExist(err) {
			// TODO(stevvooe): Very rare condition when this fails horribly,
			// such as an access error. Ideally, Exists is simple, but we may
			// consider returning an error.
			log.L.WithError(err).Fatal("error encountered checking for snapshot existence")
		}

		return false
	}

	return true
}

// Delete the snapshot idenfitied by name.
//
// If name has children, the operation will fail.
func (b *Driver) Delete(name string) error {
	panic("not implemented")
}

// TODO(stevvooe): The methods below are still in flux. We'll need to work
// out the roles of active and committed snapshots for this to become more
// clear.

// Walk the committed snapshots.
func (b *Driver) Walk(fn func(name string) error) error {
	panic("not implemented")
}

// Active will call fn for each active transaction.
func (b *Driver) Active(fn func(key string) error) error {
	panic("not implemented")
}

func hash(k string) string {
	return fmt.Sprintf("%x", sha256.Sum224([]byte(k)))
}
