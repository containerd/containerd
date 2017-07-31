// +build !windows

package containerd

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/fs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/typeurl"
	"github.com/opencontainers/image-spec/identity"
	"github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	rwm               = "rwm"
	defaultRootfsPath = "rootfs"
)

var (
	defaultEnv = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
)

func defaltCaps() []string {
	return []string{
		"CAP_CHOWN",
		"CAP_DAC_OVERRIDE",
		"CAP_FSETID",
		"CAP_FOWNER",
		"CAP_MKNOD",
		"CAP_NET_RAW",
		"CAP_SETGID",
		"CAP_SETUID",
		"CAP_SETFCAP",
		"CAP_SETPCAP",
		"CAP_NET_BIND_SERVICE",
		"CAP_SYS_CHROOT",
		"CAP_KILL",
		"CAP_AUDIT_WRITE",
	}
}

func defaultNamespaces() []specs.LinuxNamespace {
	return []specs.LinuxNamespace{
		{
			Type: specs.PIDNamespace,
		},
		{
			Type: specs.IPCNamespace,
		},
		{
			Type: specs.UTSNamespace,
		},
		{
			Type: specs.MountNamespace,
		},
		{
			Type: specs.NetworkNamespace,
		},
	}
}

func createDefaultSpec() (*specs.Spec, error) {
	s := &specs.Spec{
		Version: specs.Version,
		Root: &specs.Root{
			Path: defaultRootfsPath,
		},
		Process: &specs.Process{
			Env:             defaultEnv,
			Cwd:             "/",
			NoNewPrivileges: true,
			User: specs.User{
				UID: 0,
				GID: 0,
			},
			Capabilities: &specs.LinuxCapabilities{
				Bounding:    defaltCaps(),
				Permitted:   defaltCaps(),
				Inheritable: defaltCaps(),
				Effective:   defaltCaps(),
			},
			Rlimits: []specs.POSIXRlimit{
				{
					Type: "RLIMIT_NOFILE",
					Hard: uint64(1024),
					Soft: uint64(1024),
				},
			},
		},
		Mounts: []specs.Mount{
			{
				Destination: "/proc",
				Type:        "proc",
				Source:      "proc",
			},
			{
				Destination: "/dev",
				Type:        "tmpfs",
				Source:      "tmpfs",
				Options:     []string{"nosuid", "strictatime", "mode=755", "size=65536k"},
			},
			{
				Destination: "/dev/pts",
				Type:        "devpts",
				Source:      "devpts",
				Options:     []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620", "gid=5"},
			},
			{
				Destination: "/dev/shm",
				Type:        "tmpfs",
				Source:      "shm",
				Options:     []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"},
			},
			{
				Destination: "/dev/mqueue",
				Type:        "mqueue",
				Source:      "mqueue",
				Options:     []string{"nosuid", "noexec", "nodev"},
			},
			{
				Destination: "/sys",
				Type:        "sysfs",
				Source:      "sysfs",
				Options:     []string{"nosuid", "noexec", "nodev", "ro"},
			},
			{
				Destination: "/run",
				Type:        "tmpfs",
				Source:      "tmpfs",
				Options:     []string{"nosuid", "strictatime", "mode=755", "size=65536k"},
			},
		},
		Linux: &specs.Linux{
			// TODO (@crosbymichael) make sure we don't have have two containers in the same cgroup
			Resources: &specs.LinuxResources{
				Devices: []specs.LinuxDeviceCgroup{
					{
						Allow:  false,
						Access: rwm,
					},
				},
			},
			Namespaces: defaultNamespaces(),
		},
	}
	return s, nil
}

func WithTTY(s *specs.Spec) error {
	s.Process.Terminal = true
	s.Process.Env = append(s.Process.Env, "TERM=xterm")
	return nil
}

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
			r, err := store.Reader(ctx, ic.Digest)
			if err != nil {
				return err
			}
			if err := json.NewDecoder(r).Decode(&ociimage); err != nil {
				r.Close()
				return err
			}
			r.Close()
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

func WithHostHosts(s *specs.Spec) error {
	s.Mounts = append(s.Mounts, specs.Mount{
		Destination: "/etc/hosts",
		Type:        "bind",
		Source:      "/etc/hosts",
		Options:     []string{"rbind", "ro"},
	})
	return nil
}

func WithHostResoveconf(s *specs.Spec) error {
	s.Mounts = append(s.Mounts, specs.Mount{
		Destination: "/etc/resolv.conf",
		Type:        "bind",
		Source:      "/etc/resolv.conf",
		Options:     []string{"rbind", "ro"},
	})
	return nil
}

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

func remapRootFS(mounts []mount.Mount, uid, gid uint32) error {
	root, err := ioutil.TempDir("", "ctd-remap")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	for _, m := range mounts {
		if err := m.Mount(root); err != nil {
			return err
		}
	}
	defer unix.Unmount(root, 0)
	return filepath.Walk(root, incrementFS(root, uid, gid))
}

func incrementFS(root string, uidInc, gidInc uint32) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if root == path {
			return nil
		}
		var (
			stat    = info.Sys().(*syscall.Stat_t)
			u, g    = int(stat.Uid + uidInc), int(stat.Gid + gidInc)
			symlink = info.Mode()&os.ModeSymlink != 0
		)
		// make sure we resolve links inside the root for symlinks
		if path, err = fs.RootPath(root, strings.TrimPrefix(path, root)); err != nil {
			return err
		}
		if err := os.Chown(path, u, g); err != nil && !symlink {
			return err
		}
		return nil
	}
}
