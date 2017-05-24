// +build !windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/console"
	containersapi "github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/descriptor"
	"github.com/containerd/containerd/api/types/mount"
	mountt "github.com/containerd/containerd/mount"
	protobuf "github.com/gogo/protobuf/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

const (
	rwm               = "rwm"
	defaultRootfsPath = "rootfs"
)

var capabilities = []string{
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

func spec(id string, config *ocispec.ImageConfig, context *cli.Context, rootfs string) (*specs.Spec, error) {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	env = append(env, config.Env...)
	cmd := config.Cmd
	if v := context.Args().Tail(); len(v) > 0 {
		cmd = v
	}
	var (
		// TODO: support overriding entrypoint
		args     = append(config.Entrypoint, cmd...)
		tty      = context.Bool("tty")
		uid, gid uint32
	)
	if config.User != "" {
		parts := strings.Split(config.User, ":")
		switch len(parts) {
		case 1:
			v, err := strconv.ParseUint(parts[0], 0, 10)
			if err != nil {
				return nil, err
			}
			uid, gid = uint32(v), uint32(v)
		case 2:
			v, err := strconv.ParseUint(parts[0], 0, 10)
			if err != nil {
				return nil, err
			}
			uid = uint32(v)
			if v, err = strconv.ParseUint(parts[1], 0, 10); err != nil {
				return nil, err
			}
			gid = uint32(v)
		default:
			return nil, fmt.Errorf("invalid USER value %s", config.User)
		}
	}
	if tty {
		env = append(env, "TERM=xterm")
	}
	cwd := config.WorkingDir
	if cwd == "" {
		cwd = "/"
	}
	if rootfs == "" {
		rootfs = defaultRootfsPath
	}
	s := &specs.Spec{
		Version: specs.Version,
		Platform: specs.Platform{
			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		},
		Root: specs.Root{
			Path:     rootfs,
			Readonly: context.Bool("readonly"),
		},
		Process: specs.Process{
			Args:            args,
			Env:             env,
			Terminal:        tty,
			Cwd:             cwd,
			NoNewPrivileges: true,
			User: specs.User{
				UID: uid,
				GID: gid,
			},
			Capabilities: &specs.LinuxCapabilities{
				Bounding:    capabilities,
				Permitted:   capabilities,
				Inheritable: capabilities,
				Effective:   capabilities,
				Ambient:     capabilities,
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
		Hostname: id,
		Linux: &specs.Linux{
			Resources: &specs.LinuxResources{
				Devices: []specs.LinuxDeviceCgroup{
					{
						Allow:  false,
						Access: rwm,
					},
				},
			},
			Namespaces: []specs.LinuxNamespace{
				{
					Type: "pid",
				},
				{
					Type: "ipc",
				},
				{
					Type: "uts",
				},
				{
					Type: "mount",
				},
			},
		},
	}
	if !context.Bool("net-host") {
		s.Linux.Namespaces = append(s.Linux.Namespaces, specs.LinuxNamespace{
			Type: "network",
		})
	}
	for _, mount := range context.StringSlice("mount") {
		m, err := parseMountFlag(mount)
		if err != nil {
			return nil, err
		}

		s.Mounts = append(s.Mounts, m)
	}
	return s, nil
}

func customSpec(configPath string, rootfs string) (*specs.Spec, error) {
	b, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var s specs.Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if rootfs == "" {
		if s.Root.Path != defaultRootfsPath {
			logrus.Warnf("ignoring Root.Path %q, setting %q forcibly", s.Root.Path, defaultRootfsPath)
			s.Root.Path = defaultRootfsPath
		}
	} else {
		s.Root.Path = rootfs
	}
	return &s, nil
}

func getConfig(context *cli.Context, imageConfig *ocispec.ImageConfig, rootfs string) (*specs.Spec, error) {
	config := context.String("runtime-config")
	if config == "" {
		return spec(context.String("id"), imageConfig, context, rootfs)
	}

	return customSpec(config, rootfs)
}

func newContainerSpec(context *cli.Context, config *ocispec.ImageConfig, imageRef string) ([]byte, error) {
	s, err := getConfig(context, config, context.String("rootfs"))
	if err != nil {
		return nil, err
	}
	if s.Annotations == nil {
		s.Annotations = make(map[string]string)
	}
	s.Annotations["image"] = imageRef
	return json.Marshal(s)
}

func newCreateContainerRequest(context *cli.Context, id, snapshot string, spec []byte) (*containersapi.CreateContainerRequest, error) {
	create := &containersapi.CreateContainerRequest{
		Container: containersapi.Container{
			ID: id,
			Spec: &protobuf.Any{
				TypeUrl: specs.Version,
				Value:   spec,
			},
			Runtime: context.String("runtime"),
			RootFS:  snapshot,
		},
	}

	return create, nil
}

func newCreateTaskRequest(context *cli.Context, id, tmpDir string, checkpoint *ocispec.Descriptor, mounts []mountt.Mount) (*execution.CreateRequest, error) {
	create := &execution.CreateRequest{
		ContainerID: id,
		Terminal:    context.Bool("tty"),
		Stdin:       filepath.Join(tmpDir, "stdin"),
		Stdout:      filepath.Join(tmpDir, "stdout"),
		Stderr:      filepath.Join(tmpDir, "stderr"),
	}

	for _, m := range mounts {
		create.Rootfs = append(create.Rootfs, &mount.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}

	if checkpoint != nil {
		create.Checkpoint = &descriptor.Descriptor{
			MediaType: checkpoint.MediaType,
			Size_:     checkpoint.Size,
			Digest:    checkpoint.Digest,
		}
	}

	return create, nil
}

func handleConsoleResize(ctx context.Context, service execution.TasksClient, id string, pid uint32, con console.Console) error {
	// do an initial resize of the console
	size, err := con.Size()
	if err != nil {
		return err
	}
	if _, err := service.Pty(ctx, &execution.PtyRequest{
		ContainerID: id,
		Pid:         pid,
		Width:       uint32(size.Width),
		Height:      uint32(size.Height),
	}); err != nil {
		return err
	}
	s := make(chan os.Signal, 16)
	signal.Notify(s, unix.SIGWINCH)
	go func() {
		for range s {
			size, err := con.Size()
			if err != nil {
				logrus.WithError(err).Error("get pty size")
				continue
			}
			if _, err := service.Pty(ctx, &execution.PtyRequest{
				ContainerID: id,
				Pid:         pid,
				Width:       uint32(size.Width),
				Height:      uint32(size.Height),
			}); err != nil {
				logrus.WithError(err).Error("resize pty")
			}
		}
	}()
	return nil
}
