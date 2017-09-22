package containerd

import specs "github.com/opencontainers/runtime-spec/specs-go"

const (
	rwm               = "rwm"
	defaultRootfsPath = "rootfs"
)

var (
	defaultEnv = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
)

func defaultCaps() []string {
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

func createDefaultLinuxSpec() (*specs.Spec, error) {
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
				Bounding:    defaultCaps(),
				Permitted:   defaultCaps(),
				Inheritable: defaultCaps(),
				Effective:   defaultCaps(),
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
