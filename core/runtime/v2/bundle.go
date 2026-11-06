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

package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/runtime-spec/specs-go"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/kmutex"
	"github.com/containerd/containerd/v2/pkg/identifiers"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
)

// LoadBundle loads an existing bundle from disk
func LoadBundle(ctx context.Context, root, id string) (*Bundle, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	return &Bundle{
		ID:        id,
		Path:      filepath.Join(root, ns, id),
		Namespace: ns,
	}, nil
}

// NewBundle returns a new bundle on disk
func NewBundle(ctx context.Context, root, state, id string, spec typeurl.Any) (b *Bundle, err error) {
	if err := identifiers.Validate(id); err != nil {
		return nil, fmt.Errorf("invalid task id %s: %w", id, err)
	}

	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	work := filepath.Join(root, ns, id)
	b = &Bundle{
		ID:        id,
		Path:      filepath.Join(state, ns, id),
		Namespace: ns,
	}
	var (
		paths []string
		path  = b.Path
		gen   uint64
	)
	defer func() {
		if err != nil {
			for _, d := range paths {
				os.RemoveAll(d)
			}
			if gen != 0 {
				bundleOwners.CompareAndDelete(path, bundleOwner{gen: gen})
			}
		}
	}()
	// create state directory for the bundle
	if err := os.MkdirAll(filepath.Dir(b.Path), 0711); err != nil {
		return nil, err
	}
	if gen, err = mkdirBundle(ctx, b.Path); err != nil {
		// os.IsExist does not unwrap, so a failed cleanup of a leftover does not
		// match, though ENOTEMPTY would match errors.Is(err, os.ErrExist).
		if os.IsExist(err) {
			return nil, fmt.Errorf("bundle for %s: %w", id, errdefs.ErrAlreadyExists)
		}
		return nil, err
	}
	b.gen = gen
	if typeurl.Is(spec, &specs.Spec{}) {
		if err := prepareBundleDirectoryPermissions(b.Path, spec.GetValue()); err != nil {
			return nil, err
		}
	}
	paths = append(paths, b.Path)
	// create working directory for the bundle
	if err := os.MkdirAll(filepath.Dir(work), 0711); err != nil {
		return nil, err
	}
	rootfs := filepath.Join(b.Path, "rootfs")
	if err := os.MkdirAll(rootfs, 0711); err != nil {
		return nil, err
	}
	paths = append(paths, rootfs)
	// The bundle is ours at this point, so an existing work dir is a leftover.
	if err := mkdirReplace(ctx, work, 0711, true); err != nil {
		return nil, err
	}
	paths = append(paths, work)
	// create work link (symlink on unix, junction on windows)
	if err := createWorkLink(work, b.Path); err != nil {
		return nil, err
	}
	// Spec may be nil for some sandboxers that do not initialize the spec, for
	// example the shim sandboxer with a hostNetwork container.
	if spec != nil {
		if specVal := spec.GetValue(); specVal != nil {
			logger := log.G(ctx)
			if logger.Logger.IsLevelEnabled(log.TraceLevel) && typeurl.Is(spec, &specs.Spec{}) {
				var s specs.Spec
				if err := json.Unmarshal(specVal, &s); err == nil && s.Linux != nil {
					logger.WithFields(log.Fields{
						"bundle":        b.Path,
						"maskedPaths":   s.Linux.MaskedPaths,
						"readonlyPaths": s.Linux.ReadonlyPaths,
					}).Trace("configured bundle paths")
				}
			}
			// write the spec to the bundle
			specPath := filepath.Join(b.Path, oci.ConfigFilename)
			err = os.WriteFile(specPath, specVal, 0666)
			if err != nil {
				return nil, fmt.Errorf("failed to write bundle spec: %w", err)
			}
		}
	}
	return b, nil
}

// Bundle represents an OCI bundle
type Bundle struct {
	// ID of the bundle
	ID string
	// Path to the bundle
	Path string
	// Namespace of the bundle
	Namespace string

	// gen identifies the create of this bundle. It is zero for a loaded one.
	gen uint64
}

// bundleOwner is the latest create of a bundle path in this process.
type bundleOwner struct {
	gen uint64
	// leftover is set if the Delete of that bundle failed.
	leftover bool
}

var (
	// bundleOwners holds a bundleOwner for each bundle path that is live or
	// whose Delete failed.
	bundleOwners sync.Map
	// bundleGen generates the gen of new bundles.
	bundleGen atomic.Uint64
	// bundleLocks serializes create and delete of the same bundle path.
	bundleLocks = kmutex.New()
)

// mkdirBundle creates the bundle directory and returns the gen of the new
// bundle. An existing directory is replaced only if its own Delete failed. Any
// other belongs to a task that is live or still being created, so a second
// create for the same ID fails with "file exists" and leaves it alone.
func mkdirBundle(ctx context.Context, path string) (uint64, error) {
	if err := bundleLocks.Lock(ctx, path); err != nil {
		return 0, err
	}
	defer bundleLocks.Unlock(path)
	o, _ := bundleOwners.Load(path)
	if err := mkdirReplace(ctx, path, 0700, o != nil && o.(bundleOwner).leftover); err != nil {
		return 0, err
	}
	gen := bundleGen.Add(1)
	bundleOwners.Store(path, bundleOwner{gen: gen})
	return gen, nil
}

// Delete a bundle atomically
func (b *Bundle) Delete() error {
	// Held through the owner update, so a create never sees it stale.
	// Cannot fail without a cancelable context.
	bundleLocks.Lock(context.Background(), b.Path)
	defer bundleLocks.Unlock(b.Path)
	if o, ok := bundleOwners.Load(b.Path); ok && o.(bundleOwner).gen != b.gen {
		// A newer create replaced this bundle, so the path is not ours.
		log.L.WithField("bundle", b.Path).Warn("not deleting bundle replaced by a newer create")
		return nil
	}
	work, werr := resolveWorkLink(b.Path)
	rootfs := filepath.Join(b.Path, "rootfs")
	err := cleanRootfs(rootfs)
	if err == nil {
		err = b.removeDirs(work, werr)
	}
	if err != nil {
		bundleOwners.Store(b.Path, bundleOwner{gen: b.gen, leftover: true})
	} else {
		bundleOwners.Delete(b.Path)
	}
	return err
}

// removeDirs removes the bundle directory and the work dir it links to.
func (b *Bundle) removeDirs(work string, werr error) error {
	err := atomicDelete(b.Path)
	if err == nil {
		if werr == nil {
			return atomicDelete(work)
		}
		return nil
	}
	// error removing the bundle path; still attempt removing work dir
	var err2 error
	if werr == nil {
		err2 = atomicDelete(work)
		if err2 == nil {
			return err
		}
	}
	return fmt.Errorf("failed to remove both bundle and workdir locations: %v: %w", err2, err)
}

// mkdirReplace creates dir. If dir already exists and replace is set, the old
// one is removed first and the creation is retried once.
func mkdirReplace(ctx context.Context, dir string, perm os.FileMode, replace bool) error {
	err := os.Mkdir(dir, perm)
	if err == nil || !replace || !os.IsExist(err) {
		return err
	}
	log.G(ctx).WithField("path", dir).Warn("removing leftover directory")
	// Only a bundle has a rootfs, which must be unmounted before it is
	// removed, so that RemoveAll does not walk into a live mount.
	if err := cleanRootfs(filepath.Join(dir, "rootfs")); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("failed to remove leftover directory %q: %w", dir, err)
	}
	return os.Mkdir(dir, perm)
}

// cleanRootfs unmounts rootfs and removes the emptied directory. Removing it
// without recursion fails if a mount is still busy, so callers never delete
// through a live mount.
func cleanRootfs(rootfs string) error {
	if err := mount.UnmountRecursive(rootfs, 0); err != nil {
		return fmt.Errorf("unmount rootfs %s: %w", rootfs, err)
	}
	if err := os.Remove(rootfs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove bundle rootfs: %w", err)
	}
	return nil
}

// atomicDelete renames the path to a hidden file before removal
func atomicDelete(path string) error {
	// create a hidden dir for an atomic removal
	atomicPath := filepath.Join(filepath.Dir(path), "."+filepath.Base(path))
	// A removal that failed after the rename left atomicPath behind, which
	// would make every later rename fail.
	if err := os.RemoveAll(atomicPath); err != nil {
		return err
	}
	if err := os.Rename(path, atomicPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.RemoveAll(atomicPath)
}
