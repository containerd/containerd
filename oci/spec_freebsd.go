package oci

import (
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func defaultMounts() []specs.Mount {
	return []specs.Mount{
                {
                        Destination: "/proc",
                        Type:        "procfs",
                        Source:      "proc",
                        Options:     []string{"nosuid", "noexec"},
                },
                {
                        Destination: "/dev",
                        Type:        "devfs",
                        Source:      "devfs",
                        Options:     []string{},
                },
		{
			Destination: "/dev/fd",
			Type:	     "fdescfs",
			Source:	     "fdescfs",
			Options:     []string{},
		},
                {
                        Destination: "/dev/mqueue",
                        Type:        "mqueue",
                        Source:      "mqueue",
                        Options:     []string{"nosuid", "noexec"},
                },
                {
                        Destination: "/dev/shm",
                        Type:        "tmpfs",
                        Source:      "shm",
                        Options:     []string{"nosuid", "noexec", "mode=1777"},
                },
	}
}
