/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/failpoint"
	runcv2 "github.com/containerd/containerd/runtime/v2/runc/v2"
	"github.com/containerd/containerd/runtime/v2/shim"
	shimapi "github.com/containerd/containerd/runtime/v2/task"
	"github.com/gogo/protobuf/types"
)

const (
	ociConfigFilename = "config.json"

	failpointPrefixKey = "io.containerd.runtime.v2.shim.failpoint."
)

func newRuncServerWithFp(ctx context.Context, id string, publisher shim.Publisher, shutdown func()) (shim.Shim, error) {
	fps, err := newFailpointFromOCIAnnotation()
	if err != nil {
		return nil, err
	}

	s, err := runcv2.New(ctx, id, publisher, shutdown)
	if err != nil {
		return nil, err
	}
	return &taskServerWithFp{
		Shim: s,
		fps:  fps,
	}, nil
}

type taskServerWithFp struct {
	shim.Shim
	fps map[string]*failpoint.Failpoint
}

func (t *taskServerWithFp) State(ctx context.Context, req *shimapi.StateRequest) (*shimapi.StateResponse, error) {
	if fp, ok := t.fps["State"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.State(ctx, req)
}

func (t *taskServerWithFp) Create(ctx context.Context, req *shimapi.CreateTaskRequest) (*shimapi.CreateTaskResponse, error) {
	if fp, ok := t.fps["Create"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Create(ctx, req)
}

func (t *taskServerWithFp) Start(ctx context.Context, req *shimapi.StartRequest) (*shimapi.StartResponse, error) {
	if fp, ok := t.fps["Start"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Start(ctx, req)
}

func (t *taskServerWithFp) Delete(ctx context.Context, req *shimapi.DeleteRequest) (*shimapi.DeleteResponse, error) {
	if fp, ok := t.fps["Delete"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Delete(ctx, req)
}

func (t *taskServerWithFp) Pids(ctx context.Context, req *shimapi.PidsRequest) (*shimapi.PidsResponse, error) {
	if fp, ok := t.fps["Pids"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Pids(ctx, req)
}

func (t *taskServerWithFp) Pause(ctx context.Context, req *shimapi.PauseRequest) (*types.Empty, error) {
	if fp, ok := t.fps["Pause"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Pause(ctx, req)
}

func (t *taskServerWithFp) Resume(ctx context.Context, req *shimapi.ResumeRequest) (*types.Empty, error) {
	if fp, ok := t.fps["Resume"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Resume(ctx, req)
}

func (t *taskServerWithFp) Checkpoint(ctx context.Context, req *shimapi.CheckpointTaskRequest) (*types.Empty, error) {
	if fp, ok := t.fps["Checkpoint"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Checkpoint(ctx, req)
}

func (t *taskServerWithFp) Kill(ctx context.Context, req *shimapi.KillRequest) (*types.Empty, error) {
	if fp, ok := t.fps["Kill"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Kill(ctx, req)
}

func (t *taskServerWithFp) Exec(ctx context.Context, req *shimapi.ExecProcessRequest) (*types.Empty, error) {
	if fp, ok := t.fps["Exec"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Exec(ctx, req)
}

func (t *taskServerWithFp) ResizePty(ctx context.Context, req *shimapi.ResizePtyRequest) (*types.Empty, error) {
	if fp, ok := t.fps["ResizePty"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.ResizePty(ctx, req)
}

func (t *taskServerWithFp) CloseIO(ctx context.Context, req *shimapi.CloseIORequest) (*types.Empty, error) {
	if fp, ok := t.fps["CloseIO"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.CloseIO(ctx, req)
}

func (t *taskServerWithFp) Update(ctx context.Context, req *shimapi.UpdateTaskRequest) (*types.Empty, error) {
	if fp, ok := t.fps["Update"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Update(ctx, req)
}

func (t *taskServerWithFp) Wait(ctx context.Context, req *shimapi.WaitRequest) (*shimapi.WaitResponse, error) {
	if fp, ok := t.fps["Wait"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Wait(ctx, req)
}

func (t *taskServerWithFp) Stats(ctx context.Context, req *shimapi.StatsRequest) (*shimapi.StatsResponse, error) {
	if fp, ok := t.fps["Stats"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Stats(ctx, req)
}

func (t *taskServerWithFp) Shudown(ctx context.Context, req *shimapi.ShutdownRequest) (*types.Empty, error) {
	if fp, ok := t.fps["Shudown"]; ok {
		if err := fp.Evaluate(); err != nil {
			return nil, err
		}
	}
	return t.Shim.Shutdown(ctx, req)
}

// newFailpointFromOCIAnnotation reloads and parses the annotation from
// bundle-path/config.json.
//
// The annotation controlling task API's failpoint should be like:
//
//	io.containerd.runtime.v2.shim.failpoint.Create = 1*off->1*error(please retry)
//
// The `Create` is the shim unary API and the value of annotation is the
// failpoint control. The function will return a set of failpoint controllers.
func newFailpointFromOCIAnnotation() (map[string]*failpoint.Failpoint, error) {
	// NOTE: shim's current working dir is in bundle dir.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working dir: %w", err)
	}

	configPath := filepath.Join(cwd, ociConfigFilename)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %v: %w", configPath, err)
	}

	var spec oci.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("failed to parse oci.Spec(%v): %w", string(data), err)
	}

	res := make(map[string]*failpoint.Failpoint)
	for k, v := range spec.Annotations {
		if !strings.HasPrefix(k, failpointPrefixKey) {
			continue
		}

		methodName := strings.TrimPrefix(k, failpointPrefixKey)
		fp, err := failpoint.NewFailpoint(methodName, v)
		if err != nil {
			return nil, fmt.Errorf("failed to parse failpoint %v: %w", v, err)
		}
		res[methodName] = fp
	}
	return res, nil
}
