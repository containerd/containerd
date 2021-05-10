package oci

import (
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func defaultMounts() []specs.Mount {
	return []specs.Mount{
                        {
                                Destination: "/proc",
                                Type:        "proc",
                                Source:      "proc",
                                Options:     []string{"nosuid", "noexec", "nodev"},
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
                }
}
