// +build !windows

package containerd

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/containerd/containerd/api/services/containers/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/images"
	protobuf "github.com/gogo/protobuf/types"
	"github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	rwm               = "rwm"
	defaultRootfsPath = "rootfs"
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
		Root: specs.Root{
			Path: defaultRootfsPath,
		},
		Process: &specs.Process{
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
				Ambient:     defaltCaps(),
			},
			Rlimits: []specs.LinuxRlimit{
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
			{
				Destination: "/etc/resolv.conf",
				Type:        "bind",
				Source:      "/etc/resolv.conf",
				Options:     []string{"rbind", "ro"},
			},
			{
				Destination: "/etc/hosts",
				Type:        "bind",
				Source:      "/etc/hosts",
				Options:     []string{"rbind", "ro"},
			},
			{
				Destination: "/etc/localtime",
				Type:        "bind",
				Source:      "/etc/localtime",
				Options:     []string{"rbind", "ro"},
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
		env := []string{
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		}
		s.Process.Env = append(env, config.Env...)
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

func WithSpec(spec *specs.Spec) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		data, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		c.Spec = &protobuf.Any{
			TypeUrl: spec.Version,
			Value:   data,
		}
		return nil
	}
}

func WithResources(resources *specs.LinuxResources) UpdateTaskOpts {
	return func(ctx context.Context, client *Client, r *tasks.UpdateTaskRequest) error {
		data, err := json.Marshal(resources)
		if err != nil {
			return err
		}
		r.Resources = &protobuf.Any{
			TypeUrl: specs.Version,
			Value:   data,
		}
		return nil
	}
}
