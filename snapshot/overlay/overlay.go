package overlay

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/docker/containerd"
	"github.com/docker/containerd/snapshot"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

type Snapshotter struct {
	root  string
	links *cache
}

func NewSnapshotter(root string) (*Snapshotter, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	for _, p := range []string{
		"committed", // committed snapshots
		"active",    // active snapshots
		"index",     // snapshots by hashed name
	} {
		if err := os.MkdirAll(filepath.Join(root, p), 0700); err != nil {
			return nil, err
		}
	}
	return &Snapshotter{
		root:  root,
		links: newCache(),
	}, nil
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
func (o *Snapshotter) Stat(ctx context.Context, key string) (snapshot.Info, error) {
	path, err := o.links.get(filepath.Join(o.root, "index", hash(key)))
	if err != nil {
		if !os.IsNotExist(err) {
			return snapshot.Info{}, err
		}

		return snapshot.Info{}, errors.Errorf("snapshot %v not found", key)
	}

	// TODO(stevvooe): We don't confirm the name to avoid the lookup cost.
	return o.stat(path)
}

func (o *Snapshotter) stat(path string) (snapshot.Info, error) {
	ppath, err := o.links.get(filepath.Join(path, "parent"))
	if err != nil {
		if !os.IsNotExist(err) {
			return snapshot.Info{}, err
		}

		// no parent
	}

	kp, err := ioutil.ReadFile(filepath.Join(path, "name"))
	if err != nil {
		return snapshot.Info{}, err
	}

	var parent string
	if ppath != "" {
		p, err := ioutil.ReadFile(filepath.Join(ppath, "name"))
		if err != nil {
			return snapshot.Info{}, err
		}
		parent = string(p)
	}

	ro := true
	kind := snapshot.KindCommitted
	if strings.HasPrefix(path, filepath.Join(o.root, "active")) {
		// TODO(stevvooe): Maybe there is a better way?
		kind = snapshot.KindActive

		// TODO(stevvooe): We haven't introduced this to overlay yet.
		// We'll add it when we add tests for it.
		ro = false
	}

	return snapshot.Info{
		Name:     string(kp),
		Parent:   parent,
		Kind:     kind,
		Readonly: ro,
	}, nil
}

func (o *Snapshotter) Prepare(ctx context.Context, key, parent string) ([]containerd.Mount, error) {
	active, err := o.newActiveDir(key, false)
	if err != nil {
		return nil, err
	}
	if parent != "" {
		if err := active.setParent(parent); err != nil {
			return nil, err
		}
	}
	return active.mounts(o.links)
}

func (o *Snapshotter) View(ctx context.Context, key, parent string) ([]containerd.Mount, error) {
	active, err := o.newActiveDir(key, true)
	if err != nil {
		return nil, err
	}
	if parent != "" {
		if err := active.setParent(parent); err != nil {
			return nil, err
		}
	}
	return active.mounts(o.links)
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (o *Snapshotter) Mounts(ctx context.Context, key string) ([]containerd.Mount, error) {
	active := o.getActive(key)
	return active.mounts(o.links)
}

func (o *Snapshotter) Commit(ctx context.Context, name, key string) error {
	active := o.getActive(key)
	return active.commit(name, o.links)
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (o *Snapshotter) Remove(ctx context.Context, key string) error {
	panic("not implemented")
}

// Walk the committed snapshots.
func (o *Snapshotter) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	root := filepath.Join(o.root, "index")
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

		target, err := o.links.get(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}

		si, err := o.stat(target)
		if err != nil {
			return err
		}

		if err := fn(ctx, si); err != nil {
			return err
		}

		return nil
	})
}

func (o *Snapshotter) newActiveDir(key string, readonly bool) (*activeDir, error) {
	var (
		path      = filepath.Join(o.root, "active", hash(key))
		name      = filepath.Join(path, "name")
		indexlink = filepath.Join(o.root, "index", hash(key))
	)
	a := &activeDir{
		path:         path,
		committedDir: filepath.Join(o.root, "committed"),
		indexlink:    indexlink,
	}
	if !readonly {
		for _, p := range []string{
			"work",
			"fs",
		} {
			if err := os.MkdirAll(filepath.Join(path, p), 0700); err != nil {
				a.delete()
				return nil, err
			}
		}
	} else {
		if err := os.MkdirAll(filepath.Join(path, "fs"), 0700); err != nil {
			a.delete()
			return nil, err
		}
	}

	if err := ioutil.WriteFile(name, []byte(key), 0644); err != nil {
		a.delete()
		return nil, err
	}

	// link from namespace
	if err := os.Symlink(path, indexlink); err != nil {
		a.delete()
		return nil, err
	}

	return a, nil
}

func (o *Snapshotter) getActive(key string) *activeDir {
	return &activeDir{
		path:         filepath.Join(o.root, "active", hash(key)),
		committedDir: filepath.Join(o.root, "committed"),
		indexlink:    filepath.Join(o.root, "index", hash(key)),
	}
}

func hash(k string) string {
	return digest.FromString(k).Hex()
}

type activeDir struct {
	committedDir string
	path         string
	indexlink    string
}

func (a *activeDir) delete() error {
	return os.RemoveAll(a.path)
}

func (a *activeDir) setParent(name string) error {
	return os.Symlink(filepath.Join(a.committedDir, hash(name)), filepath.Join(a.path, "parent"))
}

func (a *activeDir) commit(name string, c *cache) error {
	if _, err := os.Stat(filepath.Join(a.path, "fs")); err != nil {
		if os.IsNotExist(err) {
			return errors.New("cannot commit view")
		}
		return err
	}

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
	c.invalidate(filepath.Join(a.path, "parent"))
	c.invalidate(a.indexlink)

	committed := filepath.Join(a.committedDir, hash(name))
	if err := os.Rename(a.path, committed); err != nil {
		return err
	}

	if err := os.Remove(a.indexlink); err != nil {
		return err
	}

	indexlink := filepath.Join(filepath.Dir(a.indexlink), hash(name))
	return os.Symlink(committed, indexlink)
}

func (a *activeDir) mounts(c *cache) ([]containerd.Mount, error) {
	var (
		parents []string
		err     error
		current = a.path
	)
	for {
		if current, err = c.get(filepath.Join(current, "parent")); err != nil {
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
		roFlag := "rw"
		if _, err := os.Stat(filepath.Join(a.path, "work")); err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
			roFlag = "ro"
		}

		return []containerd.Mount{
			{
				Source: filepath.Join(a.path, "fs"),
				Type:   "bind",
				Options: []string{
					roFlag,
					"rbind",
				},
			},
		}, nil
	}
	var options []string

	if _, err := os.Stat(filepath.Join(a.path, "work")); err == nil {
		options = append(options,
			fmt.Sprintf("workdir=%s", filepath.Join(a.path, "work")),
			fmt.Sprintf("upperdir=%s", filepath.Join(a.path, "fs")),
		)
	} else if !os.IsNotExist(err) {
		return nil, err
	} else if len(parents) == 1 {
		return []containerd.Mount{
			{
				Source: parents[0],
				Type:   "bind",
				Options: []string{
					"ro",
					"rbind",
				},
			},
		}, nil
	}

	options = append(options, fmt.Sprintf("lowerdir=%s", strings.Join(parents, ":")))
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
		links: make(map[string]string),
	}
}

type cache struct {
	mu    sync.Mutex
	links map[string]string
}

func (c *cache) get(path string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	target, ok := c.links[path]
	if !ok {
		link, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		c.links[path], target = link, link
	}
	return target, nil
}

func (c *cache) invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.links, path)
}
