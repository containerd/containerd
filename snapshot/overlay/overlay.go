package overlay

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/docker/containerd"
	digest "github.com/opencontainers/go-digest"
)

type Driver struct {
	root  string
	cache *cache
}

func NewDriver(root string) (*Driver, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	for _, p := range []string{
		"snapshots",
		"active",
	} {
		if err := os.MkdirAll(filepath.Join(root, p), 0700); err != nil {
			return nil, err
		}
	}
	return &Driver{
		root:  root,
		cache: newCache(),
	}, nil
}

func (o *Driver) Prepare(key, parent string) ([]containerd.Mount, error) {
	active, err := o.newActiveDir(key)
	if err != nil {
		return nil, err
	}
	if parent != "" {
		if err := active.setParent(parent); err != nil {
			return nil, err
		}
	}
	return o.Mounts(key)
}

func (o *Driver) View(key, parent string) ([]containerd.Mount, error) {
	panic("not implemented")
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (o *Driver) Mounts(key string) ([]containerd.Mount, error) {
	active := o.getActive(key)
	return active.mounts(o.cache)
}

func (o *Driver) Commit(name, key string) error {
	active := o.getActive(key)
	return active.commit(name, o.cache)
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (o *Driver) Remove(key string) error {
	panic("not implemented")
}

// Parent returns the parent of snapshot identified by name.
func (o *Driver) Parent(name string) (string, error) {
	ppath, err := o.cache.get(filepath.Join(o.root, "snapshots", hash(name)))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // no parent
		}

		return "", err
	}

	p, err := ioutil.ReadFile(filepath.Join(ppath, "name"))
	if err != nil {
		return "", err
	}

	return string(p), nil
}

// Exists returns true if the snapshot with name exists.
func (o *Driver) Exists(name string) bool {
	panic("not implemented")
}

// Delete the snapshot idenfitied by name.
//
// If name has children, the operation will fail.
func (o *Driver) Delete(name string) error {
	panic("not implemented")
}

// Walk the committed snapshots.
func (o *Driver) Walk(fn func(name string) error) error {
	panic("not implemented")
}

// Active will call fn for each active transaction.
func (o *Driver) Active(fn func(key string) error) error {
	panic("not implemented")
}

func (o *Driver) newActiveDir(key string) (*activeDir, error) {
	var (
		path = filepath.Join(o.root, "active", hash(key))
	)
	a := &activeDir{
		path:         path,
		snapshotsDir: filepath.Join(o.root, "snapshots"),
	}
	for _, p := range []string{
		"work",
		"fs",
	} {
		if err := os.MkdirAll(filepath.Join(path, p), 0700); err != nil {
			a.delete()
			return nil, err
		}
	}
	return a, nil
}

func (o *Driver) getActive(key string) *activeDir {
	return &activeDir{
		path:         filepath.Join(o.root, "active", hash(key)),
		snapshotsDir: filepath.Join(o.root, "snapshots"),
	}
}

func hash(k string) string {
	return digest.FromString(k).Hex()
}

type activeDir struct {
	snapshotsDir string
	path         string
}

func (a *activeDir) delete() error {
	return os.RemoveAll(a.path)
}

func (a *activeDir) setParent(name string) error {
	return os.Symlink(filepath.Join(a.snapshotsDir, hash(name)), filepath.Join(a.path, "parent"))
}

func (a *activeDir) commit(name string, c *cache) error {
	// TODO(stevvooe): This doesn't quite meet the current model. The new model
	// is to copy all of this out and let the transaction continue. We don't
	// really have tests for it yet, but this will be the spot to fix it.
	//
	// Nothing should be removed until remove is called on the active
	// transaction.
	if err := os.RemoveAll(filepath.Join(a.path, "work")); err != nil {
		return err
	}

	if err := ioutil.WriteFile(filepath.Join(a.path, "name"), []byte(name), 0644); err != nil {
		return err
	}

	c.invalidate(a.path) // clears parent cache, since we end up moving.
	return os.Rename(a.path, filepath.Join(a.snapshotsDir, hash(name)))
}

func (a *activeDir) mounts(c *cache) ([]containerd.Mount, error) {
	var (
		parents []string
		err     error
		current = a.path
	)
	for {
		if current, err = c.get(current); err != nil {
			if os.IsNotExist(err) {
				break
			}
			return nil, err
		}
		parents = append(parents, filepath.Join(current, "fs"))
	}
	if len(parents) == 0 {
		// if we only have one layer/no parents then just return a bind mount as overlay
		// will not work
		return []containerd.Mount{
			{
				Source: filepath.Join(a.path, "fs"),
				Type:   "bind",
				Options: []string{
					"rw",
					"rbind",
				},
			},
		}, nil
	}
	options := []string{
		fmt.Sprintf("workdir=%s", filepath.Join(a.path, "work")),
		fmt.Sprintf("upperdir=%s", filepath.Join(a.path, "fs")),
		fmt.Sprintf("lowerdir=%s", strings.Join(parents, ":")),
	}
	return []containerd.Mount{
		{
			Type:    "overlay",
			Source:  "overlay",
			Options: options,
		},
	}, nil
}

func newCache() *cache {
	return &cache{
		parents: make(map[string]string),
	}
}

type cache struct {
	mu      sync.Mutex
	parents map[string]string
}

func (c *cache) get(path string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	parentRoot, ok := c.parents[path]
	if !ok {
		link, err := os.Readlink(filepath.Join(path, "parent"))
		if err != nil {
			return "", err
		}
		c.parents[path], parentRoot = link, link
	}
	return parentRoot, nil
}

func (c *cache) invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.parents, path)
}
