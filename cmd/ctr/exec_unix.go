// +build !windows

package main

import (
	"encoding/json"
	"path/filepath"

	"github.com/containerd/containerd/api/services/execution"
	protobuf "github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

func createProcessSpec(args []string, cwd string, tty bool) specs.Process {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	if tty {
		env = append(env, "TERM=xterm")
	}
	if cwd == "" {
		cwd = "/"
	}
	return specs.Process{
		Args:            args,
		Env:             env,
		Terminal:        tty,
		Cwd:             cwd,
		NoNewPrivileges: true,
		User: specs.User{
			UID: 0,
			GID: 0,
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
	}
}

func newExecRequest(context *cli.Context, tmpDir, id string) (*execution.ExecRequest, error) {
	process := createProcessSpec(context.Args(), context.String("cwd"), context.Bool("tty"))
	data, err := json.Marshal(process)
	if err != nil {
		return nil, err
	}
	return &execution.ExecRequest{
		ContainerID: id,
		Spec: &protobuf.Any{
			TypeUrl: specs.Version,
			Value:   data,
		},
		Terminal: context.Bool("tty"),
		Stdin:    filepath.Join(tmpDir, "stdin"),
		Stdout:   filepath.Join(tmpDir, "stdout"),
		Stderr:   filepath.Join(tmpDir, "stderr"),
	}, nil
}
