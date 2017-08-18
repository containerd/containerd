// +build !windows

package containerd

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/typeurl"
	"github.com/opencontainers/image-spec/identity"
	"github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// WithTTY sets the information on the spec as well as the environment variables for
// using a TTY
func WithTTY(s *specs.Spec) error {
	s.Process.Terminal = true
	s.Process.Env = append(s.Process.Env, "TERM=xterm")
	return nil
}

// WithHostNamespace allows a task to run inside the host's linux namespace
func WithHostNamespace(ns specs.LinuxNamespaceType) SpecOpts {
	return func(s *specs.Spec) error {
		for i, n := range s.Linux.Namespaces {
			if n.Type == ns {
				s.Linux.Namespaces = append(s.Linux.Namespaces[:i], s.Linux.Namespaces[i+1:]...)
				return nil
			}
		}
		return nil
	}
}

// WithLinuxNamespace uses the passed in namespace for the spec. If a namespace of the same type already exists in the
// spec, the existing namespace is replaced by the one provided.
func WithLinuxNamespace(ns specs.LinuxNamespace) SpecOpts {
	return func(s *specs.Spec) error {
		for i, n := range s.Linux.Namespaces {
			if n.Type == ns.Type {
				before := s.Linux.Namespaces[:i]
				after := s.Linux.Namespaces[i+1:]
				s.Linux.Namespaces = append(before, ns)
				s.Linux.Namespaces = append(s.Linux.Namespaces, after...)
				return nil
			}
		}
		s.Linux.Namespaces = append(s.Linux.Namespaces, ns)
		return nil
	}
}

// WithImageConfig configures the spec to from the configuration of an Image
func WithImageConfig(ctx context.Context, i Image) SpecOpts {
	return func(s *specs.Spec) error {
		var (
			image = i.(*image)
			store = image.client.ContentStore()
		)
		ic, err := image.i.Config(ctx, store)
		if err != nil {
			return err
		}
		var (
			ociimage v1.Image
			config   v1.ImageConfig
		)
		switch ic.MediaType {
		case v1.MediaTypeImageConfig, images.MediaTypeDockerSchema2Config:
			p, err := content.ReadBlob(ctx, store, ic.Digest)
			if err != nil {
				return err
			}

			if err := json.Unmarshal(p, &ociimage); err != nil {
				return err
			}
			config = ociimage.Config
		default:
			return fmt.Errorf("unknown image config media type %s", ic.MediaType)
		}
		s.Process.Env = append(s.Process.Env, config.Env...)
		var (
			uid, gid uint32
		)
		cmd := config.Cmd
		s.Process.Args = append(config.Entrypoint, cmd...)
		if config.User != "" {
			parts := strings.Split(config.User, ":")
			switch len(parts) {
			case 1:
				v, err := strconv.ParseUint(parts[0], 0, 10)
				if err != nil {
					return err
				}
				uid, gid = uint32(v), uint32(v)
			case 2:
				v, err := strconv.ParseUint(parts[0], 0, 10)
				if err != nil {
					return err
				}
				uid = uint32(v)
				if v, err = strconv.ParseUint(parts[1], 0, 10); err != nil {
					return err
				}
				gid = uint32(v)
			default:
				return fmt.Errorf("invalid USER value %s", config.User)
			}
		}
		s.Process.User.UID, s.Process.User.GID = uid, gid
		cwd := config.WorkingDir
		if cwd == "" {
			cwd = "/"
		}
		s.Process.Cwd = cwd
		return nil
	}
}

// WithRootFSPath specifies unmanaged rootfs path.
func WithRootFSPath(path string, readonly bool) SpecOpts {
	return func(s *specs.Spec) error {
		s.Root = &specs.Root{
			Path:     path,
			Readonly: readonly,
		}
		// Entrypoint is not set here (it's up to caller)
		return nil
	}
}

// WithSpec sets the provided spec for a new container
func WithSpec(spec *specs.Spec) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		any, err := typeurl.MarshalAny(spec)
		if err != nil {
			return err
		}
		c.Spec = any
		return nil
	}
}

// WithResources sets the provided resources on the spec for task updates
func WithResources(resources *specs.LinuxResources) UpdateTaskOpts {
	return func(ctx context.Context, client *Client, r *UpdateTaskInfo) error {
		r.Resources = resources
		return nil
	}
}

// WithNoNewPrivileges sets no_new_privileges on the process for the container
func WithNoNewPrivileges(s *specs.Spec) error {
	s.Process.NoNewPrivileges = true
	return nil
}

// WithHostHostsFile bind-mounts the host's /etc/hosts into the container as readonly
func WithHostHostsFile(s *specs.Spec) error {
	s.Mounts = append(s.Mounts, specs.Mount{
		Destination: "/etc/hosts",
		Type:        "bind",
		Source:      "/etc/hosts",
		Options:     []string{"rbind", "ro"},
	})
	return nil
}

// WithHostResolvconf bind-mounts the host's /etc/resolv.conf into the container as readonly
func WithHostResolvconf(s *specs.Spec) error {
	s.Mounts = append(s.Mounts, specs.Mount{
		Destination: "/etc/resolv.conf",
		Type:        "bind",
		Source:      "/etc/resolv.conf",
		Options:     []string{"rbind", "ro"},
	})
	return nil
}

// WithHostLocaltime bind-mounts the host's /etc/localtime into the container as readonly
func WithHostLocaltime(s *specs.Spec) error {
	s.Mounts = append(s.Mounts, specs.Mount{
		Destination: "/etc/localtime",
		Type:        "bind",
		Source:      "/etc/localtime",
		Options:     []string{"rbind", "ro"},
	})
	return nil
}

// WithUserNamespace sets the uid and gid mappings for the task
// this can be called multiple times to add more mappings to the generated spec
func WithUserNamespace(container, host, size uint32) SpecOpts {
	return func(s *specs.Spec) error {
		var hasUserns bool
		for _, ns := range s.Linux.Namespaces {
			if ns.Type == specs.UserNamespace {
				hasUserns = true
				break
			}
		}
		if !hasUserns {
			s.Linux.Namespaces = append(s.Linux.Namespaces, specs.LinuxNamespace{
				Type: specs.UserNamespace,
			})
		}
		mapping := specs.LinuxIDMapping{
			ContainerID: container,
			HostID:      host,
			Size:        size,
		}
		s.Linux.UIDMappings = append(s.Linux.UIDMappings, mapping)
		s.Linux.GIDMappings = append(s.Linux.GIDMappings, mapping)
		return nil
	}
}

// WithRemappedSnapshot creates a new snapshot and remaps the uid/gid for the
// filesystem to be used by a container with user namespaces
func WithRemappedSnapshot(id string, i Image, uid, gid uint32) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		diffIDs, err := i.(*image).i.RootFS(ctx, client.ContentStore())
		if err != nil {
			return err
		}
		var (
			snapshotter = client.SnapshotService(c.Snapshotter)
			parent      = identity.ChainID(diffIDs).String()
			usernsID    = fmt.Sprintf("%s-%d-%d", parent, uid, gid)
		)
		if _, err := snapshotter.Stat(ctx, usernsID); err == nil {
			if _, err := snapshotter.Prepare(ctx, id, usernsID); err != nil {
				return err
			}
			c.RootFS = id
			c.Image = i.Name()
			return nil
		}
		mounts, err := snapshotter.Prepare(ctx, usernsID+"-remap", parent)
		if err != nil {
			return err
		}
		if err := remapRootFS(mounts, uid, gid); err != nil {
			snapshotter.Remove(ctx, usernsID)
			return err
		}
		if err := snapshotter.Commit(ctx, usernsID, usernsID+"-remap"); err != nil {
			return err
		}
		if _, err := snapshotter.Prepare(ctx, id, usernsID); err != nil {
			return err
		}
		c.RootFS = id
		c.Image = i.Name()
		return nil
	}
}

// WithCgroup sets the container's cgroup path
func WithCgroup(path string) SpecOpts {
	return func(s *specs.Spec) error {
		s.Linux.CgroupsPath = path
		return nil
	}
}

// WithNamespacedCgroup uses the namespace set on the context to create a
// root directory for containers in the cgroup with the id as the subcgroup
func WithNamespacedCgroup(ctx context.Context, id string) SpecOpts {
	return func(s *specs.Spec) error {
		namespace, err := namespaces.NamespaceRequired(ctx)
		if err != nil {
			return err
		}
		s.Linux.CgroupsPath = filepath.Join("/", namespace, id)
		return nil
	}
}
