package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/containerd/containerd/api/services/execution"
	protobuf "github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

func newExecRequest(context *cli.Context, tmpDir, id string) (*execution.ExecRequest, error) {
	process := specs.Process{
		Args:     context.Args(),
		Terminal: context.Bool("tty"),
		Cwd:      context.String("cwd"),
	}
	data, err := json.Marshal(process)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixNano()
	request := &execution.ExecRequest{
		ContainerID: id,
		Spec: &protobuf.Any{
			TypeUrl: specs.Version,
			Value:   data,
		},
		Terminal: context.Bool("tty"),
		Stdin:    fmt.Sprintf(`%s\ctr-%s-stdin-%d`, pipeRoot, id, now),
		Stdout:   fmt.Sprintf(`%s\ctr-%s-stdout-%d`, pipeRoot, id, now),
	}
	if !request.Terminal {
		request.Stderr = fmt.Sprintf(`%s\ctr-%s-stderr-%d`, pipeRoot, id, now)
	}

	return request, nil
}
