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

package v2

import (
	"context"
	"errors"
	"fmt"

	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"

	"github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/pkg/identifiers"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
)

type remoteTask struct {
	id     string
	client TaskServiceClient
}

func (r *remoteTask) Create(ctx context.Context, bundle string, opts runtime.CreateOpts) error {
	topts := opts.TaskOptions
	if topts == nil || topts.GetValue() == nil {
		topts = opts.RuntimeOptions
	}
	request := &task.CreateTaskRequest{
		ID:         r.id,
		Bundle:     bundle,
		Stdin:      opts.IO.Stdin,
		Stdout:     opts.IO.Stdout,
		Stderr:     opts.IO.Stderr,
		Terminal:   opts.IO.Terminal,
		Checkpoint: opts.Checkpoint,
		Options:    typeurl.MarshalProto(topts),
	}
	for _, m := range opts.Rootfs {
		request.Rootfs = append(request.Rootfs, &types.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Target:  m.Target,
			Options: m.Options,
		})
	}

	_, err := r.client.Create(ctx, request)
	if err != nil {
		return errgrpc.ToNative(err)
	}

	return nil
}

func (r *remoteTask) State(ctx context.Context) (runtime.State, error) {
	response, err := r.client.State(ctx, &task.StateRequest{
		ID: r.id,
	})
	if err != nil {
		if !errors.Is(err, ttrpc.ErrClosed) {
			return runtime.State{}, errgrpc.ToNative(err)
		}
		return runtime.State{}, errdefs.ErrNotFound
	}
	return runtime.State{
		Pid:        response.Pid,
		Status:     statusFromProto(response.Status),
		Stdin:      response.Stdin,
		Stdout:     response.Stdout,
		Stderr:     response.Stderr,
		Terminal:   response.Terminal,
		ExitStatus: response.ExitStatus,
		ExitedAt:   protobuf.FromTimestamp(response.ExitedAt),
	}, nil
}

func (r *remoteTask) Kill(ctx context.Context, signal uint32, all bool) error {
	if _, err := r.client.Kill(ctx, &task.KillRequest{
		ID:     r.id,
		Signal: signal,
		All:    all,
	}); err != nil {
		return errgrpc.ToNative(err)
	}
	return nil
}

func (r *remoteTask) ResizePty(ctx context.Context, size runtime.ConsoleSize) error {
	_, err := r.client.ResizePty(ctx, &task.ResizePtyRequest{
		ID:     r.id,
		Width:  size.Width,
		Height: size.Height,
	})
	if err != nil {
		return errgrpc.ToNative(err)
	}
	return nil
}

func (r *remoteTask) CloseIO(ctx context.Context) error {
	_, err := r.client.CloseIO(ctx, &task.CloseIORequest{
		ID:    r.id,
		Stdin: true,
	})
	if err != nil {
		return errgrpc.ToNative(err)
	}
	return nil
}

func (r *remoteTask) Start(ctx context.Context) error {
	_, err := r.client.Start(ctx, &task.StartRequest{
		ID: r.id,
	})
	if err != nil {
		return errgrpc.ToNative(err)
	}
	return nil
}

func (r *remoteTask) Wait(ctx context.Context) (*runtime.Exit, error) {
	taskPid, err := r.PID(ctx)
	if err != nil {
		return nil, err
	}
	response, err := r.client.Wait(ctx, &task.WaitRequest{
		ID: r.id,
	})
	if err != nil {
		return nil, errgrpc.ToNative(err)
	}
	return &runtime.Exit{
		Pid:       taskPid,
		Timestamp: protobuf.FromTimestamp(response.ExitedAt),
		Status:    response.ExitStatus,
	}, nil
}

func (r *remoteTask) PID(ctx context.Context) (uint32, error) {
	response, err := r.client.Connect(ctx, &task.ConnectRequest{
		ID: r.id,
	})
	if err != nil {
		return 0, errgrpc.ToNative(err)
	}

	return response.TaskPid, nil
}

func (r *remoteTask) Pause(ctx context.Context) error {
	if _, err := r.client.Pause(ctx, &task.PauseRequest{
		ID: r.id,
	}); err != nil {
		return errgrpc.ToNative(err)
	}
	return nil
}

func (r *remoteTask) Resume(ctx context.Context) error {
	if _, err := r.client.Resume(ctx, &task.ResumeRequest{
		ID: r.id,
	}); err != nil {
		return errgrpc.ToNative(err)
	}
	return nil
}

func (r *remoteTask) Exec(ctx context.Context, id string, opts runtime.ExecOpts) (runtime.ExecProcess, error) {
	if err := identifiers.Validate(id); err != nil {
		return nil, fmt.Errorf("invalid exec id %s: %w", id, err)
	}
	request := &task.ExecProcessRequest{
		ID:       r.id,
		ExecID:   id,
		Stdin:    opts.IO.Stdin,
		Stdout:   opts.IO.Stdout,
		Stderr:   opts.IO.Stderr,
		Terminal: opts.IO.Terminal,
		Spec:     opts.Spec,
	}
	if _, err := r.client.Exec(ctx, request); err != nil {
		return nil, errgrpc.ToNative(err)
	}
	return &process{
		id:   id,
		task: r,
	}, nil
}

func (r *remoteTask) Pids(ctx context.Context) ([]runtime.ProcessInfo, error) {
	resp, err := r.client.Pids(ctx, &task.PidsRequest{
		ID: r.id,
	})
	if err != nil {
		return nil, errgrpc.ToNative(err)
	}
	var processList []runtime.ProcessInfo
	for _, p := range resp.Processes {
		processList = append(processList, runtime.ProcessInfo{
			Pid:  p.Pid,
			Info: p.Info,
		})
	}
	return processList, nil
}

func (r *remoteTask) Checkpoint(ctx context.Context, path string, opts *ptypes.Any) error {
	request := &task.CheckpointTaskRequest{
		ID:      r.id,
		Path:    path,
		Options: opts,
	}
	if _, err := r.client.Checkpoint(ctx, request); err != nil {
		return errgrpc.ToNative(err)
	}
	return nil
}

func (r *remoteTask) Update(ctx context.Context, resources *ptypes.Any, annotations map[string]string) error {
	if _, err := r.client.Update(ctx, &task.UpdateTaskRequest{
		ID:          r.id,
		Resources:   resources,
		Annotations: annotations,
	}); err != nil {
		return errgrpc.ToNative(err)
	}
	return nil
}

func (r *remoteTask) Process(ctx context.Context, id string) (runtime.ExecProcess, error) {
	p := &process{
		id:   id,
		task: r,
	}
	if _, err := p.State(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func (r *remoteTask) Stats(ctx context.Context) (*ptypes.Any, error) {
	response, err := r.client.Stats(ctx, &task.StatsRequest{
		ID: r.id,
	})
	if err != nil {
		return nil, errgrpc.ToNative(err)
	}
	return response.Stats, nil
}
