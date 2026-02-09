//go:build linux

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

package task

import (
	"context"
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	taskAPI "github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/core/events"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/shutdown"
)

type mockPublisher struct {
	events []events.Event
}

func (m *mockPublisher) Publish(ctx context.Context, topic string, event events.Event) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockPublisher) Close() error {
	return nil
}

func FuzzTaskService(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		ff := fuzz.NewConsumer(data)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx = namespaces.WithNamespace(ctx, "fuzz")

		publisher := &mockPublisher{}
		_, sd := shutdown.WithShutdown(ctx)
		defer sd.Shutdown()

		s, err := NewTaskService(ctx, publisher, sd)
		if err != nil {
			return
		}

		numOps, err := ff.GetInt()
		if err != nil {
			return
		}
		numOps = numOps % 10
		if numOps < 0 {
			numOps = -numOps
		}

		for i := 0; i < numOps; i++ {
			op, err := ff.GetInt()
			if err != nil {
				return
			}

			switch op % 17 {
			case 0:
				req := &taskAPI.CreateTaskRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Create(ctx, req)
			case 1:
				req := &taskAPI.StartRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Start(ctx, req)
			case 2:
				req := &taskAPI.DeleteRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Delete(ctx, req)
			case 3:
				req := &taskAPI.ExecProcessRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Exec(ctx, req)
			case 4:
				req := &taskAPI.ResizePtyRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.ResizePty(ctx, req)
			case 5:
				req := &taskAPI.StateRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.State(ctx, req)
			case 6:
				req := &taskAPI.PauseRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Pause(ctx, req)
			case 7:
				req := &taskAPI.ResumeRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Resume(ctx, req)
			case 8:
				req := &taskAPI.KillRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Kill(ctx, req)
			case 9:
				req := &taskAPI.PidsRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Pids(ctx, req)
			case 10:
				req := &taskAPI.CloseIORequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.CloseIO(ctx, req)
			case 11:
				req := &taskAPI.CheckpointTaskRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Checkpoint(ctx, req)
			case 12:
				req := &taskAPI.UpdateTaskRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Update(ctx, req)
			case 13:
				req := &taskAPI.WaitRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Wait(ctx, req)
			case 14:
				req := &taskAPI.ConnectRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Connect(ctx, req)
			case 15:
				req := &taskAPI.ShutdownRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Shutdown(ctx, req)
			case 16:
				req := &taskAPI.StatsRequest{}
				if err := ff.GenerateStruct(req); err != nil {
					return
				}
				_, _ = s.Stats(ctx, req)
			}
		}
	})
}
