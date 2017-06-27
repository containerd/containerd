package namespaced

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/snapshot"
)

type snapshotter struct {
	snapshot.Snapshotter
}

// NewSnapshotter returns a new Snapshotter which namespaces the given snapshot
// using the provided name and metadata store.
func NewSnapshotter(sn snapshot.Snapshotter) snapshot.Snapshotter {
	if _, ok := sn.(*snapshotter); ok {
		// Do not support double wrapping of the namespace since
		// the namespace keys are not a hierachy nor stripped from
		// the context when passed along.
		return sn
	}
	return &snapshotter{
		Snapshotter: sn,
	}
}

func snapshotKey(namespace, key string) string {
	return fmt.Sprintf("%s/%s", url.PathEscape(namespace), key)
}

func trimNamespace(key string) string {
	idx := strings.IndexRune(key, '/')
	if idx < 0 {
		return key
	}
	return key[idx+1:]
}

func splitNamespace(key string) (string, string, error) {
	idx := strings.IndexRune(key, '/')
	if idx < 0 {
		return "", key, nil
	}
	namespace, err := url.PathUnescape(key[:idx])
	if err != nil {
		return "", "", err
	}
	key = key[idx+1:]
	return namespace, key, nil
}

func resolveKey(ctx context.Context, key string) (string, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return "", err
	}

	return snapshotKey(namespace, key), nil
}

func (s *snapshotter) Stat(ctx context.Context, key string) (snapshot.Info, error) {
	key, err := resolveKey(ctx, key)
	if err != nil {
		return snapshot.Info{}, err
	}
	info, err := s.Snapshotter.Stat(ctx, key)
	if err != nil {
		return snapshot.Info{}, err
	}
	info.Name = trimNamespace(info.Name)
	if info.Parent != "" {
		info.Parent = trimNamespace(info.Parent)
	}

	return info, nil
}

func (s *snapshotter) Usage(ctx context.Context, key string) (snapshot.Usage, error) {
	key, err := resolveKey(ctx, key)
	if err != nil {
		return snapshot.Usage{}, err
	}
	return s.Snapshotter.Usage(ctx, key)
}

func (s *snapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	key, err := resolveKey(ctx, key)
	if err != nil {
		return nil, err
	}
	return s.Snapshotter.Mounts(ctx, key)
}

func (s *snapshotter) Prepare(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	key = snapshotKey(namespace, key)
	if parent != "" {
		parent = snapshotKey(namespace, parent)
	}

	return s.Snapshotter.Prepare(ctx, key, parent)
}

func (s *snapshotter) View(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	key = snapshotKey(namespace, key)
	if parent != "" {
		parent = snapshotKey(namespace, parent)
	}

	return s.Snapshotter.View(ctx, key, parent)
}

func (s *snapshotter) Commit(ctx context.Context, name, key string) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	name = snapshotKey(namespace, name)
	key = snapshotKey(namespace, key)

	return s.Snapshotter.Commit(ctx, name, key)
}

func (s *snapshotter) Remove(ctx context.Context, key string) error {
	key, err := resolveKey(ctx, key)
	if err != nil {
		return err
	}
	return s.Snapshotter.Remove(ctx, key)
}

func (s *snapshotter) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	var wrapped func(context.Context, snapshot.Info) error
	if namespace, ok := namespaces.Namespace(ctx); ok {
		wrapped = func(ctx context.Context, info snapshot.Info) error {
			ns, name, err := splitNamespace(info.Name)
			if err != nil {
				return err
			}
			if ns != namespace {
				return nil
			}
			info.Name = name
			if info.Parent != "" {
				info.Parent = trimNamespace(info.Parent)
			}

			return fn(ctx, info)
		}
	} else {
		wrapped = func(ctx context.Context, info snapshot.Info) error {
			ns, name, err := splitNamespace(info.Name)
			if err != nil {
				return err
			}
			info.Name = name
			if info.Parent != "" {
				info.Parent = trimNamespace(info.Parent)
			}

			return fn(namespaces.WithNamespace(ctx, ns), info)
		}
	}
	return s.Snapshotter.Walk(ctx, wrapped)
}
