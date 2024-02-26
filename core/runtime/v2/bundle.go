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
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/runtime-spec/specs-go"

	"github.com/containerd/containerd/v2/core/mount"
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
	var paths []string
	defer func() {
		if err != nil {
			for _, d := range paths {
				os.RemoveAll(d)
			}
		}
	}()
	// create state directory for the bundle
	if err := os.MkdirAll(filepath.Dir(b.Path), 0711); err != nil {
		return nil, err
	}
	if err := os.Mkdir(b.Path, 0700); err != nil {
		return nil, err
	}
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
	if err := os.Mkdir(work, 0711); err != nil {
		if !os.IsExist(err) {
			return nil, err
		}
		os.RemoveAll(work)
		if err := os.Mkdir(work, 0711); err != nil {
			return nil, err
		}
	}
	paths = append(paths, work)
	// symlink workdir
	if err := os.Symlink(work, filepath.Join(b.Path, "work")); err != nil {
		return nil, err
	}
	if spec := spec.GetValue(); spec != nil {
		// write the spec to the bundle
		specPath := filepath.Join(b.Path, oci.ConfigFilename)
		err = os.WriteFile(specPath, spec, 0666)
		if err != nil {
			return nil, fmt.Errorf("failed to write bundle spec: %w", err)
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
}

// Delete a bundle atomically
func (b *Bundle) Delete() error {
	work, werr := os.Readlink(filepath.Join(b.Path, "work"))
	rootfs := filepath.Join(b.Path, "rootfs")
	if runtime.GOOS != "darwin" {
		if err := mount.UnmountRecursive(rootfs, 0); err != nil {
			return fmt.Errorf("unmount rootfs %s: %w", rootfs, err)
		}
	}
	if err := os.Remove(rootfs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove bundle rootfs: %w", err)
	}
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

// atomicDelete renames the path to a hidden file before removal
func atomicDelete(path string) error {
	// create a hidden dir for an atomic removal
	atomicPath := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s", filepath.Base(path)))
	if err := os.Rename(path, atomicPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.RemoveAll(atomicPath)
}

type traverseFn func(context.Context, *Bundle) error

// TraversBundles traverse the bundles in the stateDir,
// if traverse function returns error, the bundle will be cleaned up
func TraversBundles(ctx context.Context, stateDir string, traverse traverseFn) error {
	nsDirs, err := os.ReadDir(stateDir)
	if err != nil {
		return err
	}
	for _, nsd := range nsDirs {
		if !nsd.IsDir() {
			continue
		}
		ns := nsd.Name()
		// skip hidden directories
		if len(ns) > 0 && ns[0] == '.' {
			continue
		}
		log.G(ctx).
			WithField("namespace", ns).
			WithField("stateDir", stateDir).
			Debug("traverse bundles in namespace")
		if err := traverseNamespacedBundles(namespaces.WithNamespace(ctx, ns), stateDir, traverse); err != nil {
			log.G(ctx).
				WithField("namespace", ns).
				WithField("stateDir", stateDir).
				WithError(err).Error("traverse in namespace")
			continue
		}
	}
	return nil
}

func traverseNamespacedBundles(ctx context.Context, stateDir string, traverse traverseFn) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	ctx = log.WithLogger(ctx, log.G(ctx).WithField("namespace", ns))

	bundlePaths, err := os.ReadDir(filepath.Join(stateDir, ns))
	if err != nil {
		return err
	}
	for _, sd := range bundlePaths {
		if !sd.IsDir() {
			continue
		}
		id := sd.Name()
		// skip hidden directories
		if len(id) > 0 && id[0] == '.' {
			continue
		}
		bundle, err := LoadBundle(ctx, stateDir, id)
		if err != nil {
			// fine to return error here, it is a programmer error if the context
			// does not have a namespace
			return err
		}
		// fast path
		f, err := os.Open(bundle.Path)
		if err != nil {
			bundle.Delete()
			log.G(ctx).WithError(err).Errorf("fast path read bundle path for %s", bundle.Path)
			continue
		}

		bf, err := f.Readdirnames(-1)
		f.Close()
		if err != nil {
			bundle.Delete()
			log.G(ctx).WithError(err).Errorf("fast path read bundle path for %s", bundle.Path)
			continue
		}
		if len(bf) == 0 {
			bundle.Delete()
			continue
		}
		if err := traverse(ctx, bundle); err != nil {
			log.G(ctx).WithError(err).Errorf("failed to traverse %s", bundle.Path)
			bundle.Delete()
			continue
		}

	}
	return nil
}

type traverseWorkDirFn func(ctx context.Context, ns string, dir string) error

func TraversWorkDirs(ctx context.Context, rootDir string, traverse traverseWorkDirFn) error {
	nsDirs, err := os.ReadDir(rootDir)
	if err != nil {
		return err
	}
	for _, nsd := range nsDirs {
		if !nsd.IsDir() {
			continue
		}
		ns := nsd.Name()
		// skip hidden directories
		if len(ns) > 0 && ns[0] == '.' {
			continue
		}

		f, err := os.Open(filepath.Join(rootDir, ns))
		if err != nil {
			log.G(ctx).WithError(err).Warnf("failed to open %s in %s", ns, rootDir)
			continue
		}
		defer f.Close()

		dirs, err := f.Readdirnames(-1)
		if err != nil {
			continue
		}

		for _, dir := range dirs {
			if err := traverse(ctx, ns, dir); err != nil {
				log.G(ctx).WithError(err).Errorf("failed to traverse %s", dir)
			}
		}
	}
	return nil
}
