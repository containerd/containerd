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

package sandbox

import (
	"context"
	"testing"
	"time"

	sandboxapi "github.com/containerd/containerd/api/runtime/sandbox/v1"
)

type testShutdownService struct {
	ch chan struct{}
}

func newTestShutdownService() *testShutdownService {
	return &testShutdownService{ch: make(chan struct{}, 1)}
}

func (t *testShutdownService) Shutdown() {
	select {
	case t.ch <- struct{}{}:
	default:
	}
}

func (t *testShutdownService) RegisterCallback(func(context.Context) error) {}

func (t *testShutdownService) Done() <-chan struct{} {
	return t.ch
}

func (t *testShutdownService) Err() error {
	return nil
}

func TestSandboxLifecycle(t *testing.T) {
	sd := newTestShutdownService()
	svc := newService(sd)
	ctx := context.Background()

	_, err := svc.CreateSandbox(ctx, &sandboxapi.CreateSandboxRequest{
		SandboxID:   "sandbox-1",
		BundlePath:  "/tmp/bundle",
		NetnsPath:   "/var/run/netns/test",
		Annotations: map[string]string{"example": "true"},
	})
	if err != nil {
		t.Fatalf("CreateSandbox error = %v", err)
	}

	startResp, err := svc.StartSandbox(ctx, &sandboxapi.StartSandboxRequest{
		SandboxID: "sandbox-1",
	})
	if err != nil {
		t.Fatalf("StartSandbox error = %v", err)
	}
	if startResp.GetPid() == 0 {
		t.Fatal("StartSandbox pid = 0, want non-zero")
	}

	platformResp, err := svc.Platform(ctx, &sandboxapi.PlatformRequest{
		SandboxID: "sandbox-1",
	})
	if err != nil {
		t.Fatalf("Platform error = %v", err)
	}
	if platformResp.GetPlatform() == nil {
		t.Fatal("Platform returned nil platform")
	}

	statusResp, err := svc.SandboxStatus(ctx, &sandboxapi.SandboxStatusRequest{
		SandboxID: "sandbox-1",
		Verbose:   true,
	})
	if err != nil {
		t.Fatalf("SandboxStatus error = %v", err)
	}
	if statusResp.GetState() != "RUNNING" {
		t.Fatalf("SandboxStatus state = %q, want RUNNING", statusResp.GetState())
	}
	if got := statusResp.GetInfo()["annotation.example"]; got != "true" {
		t.Fatalf("SandboxStatus annotation.example = %q, want true", got)
	}

	waitDone := make(chan *sandboxapi.WaitSandboxResponse, 1)
	waitErr := make(chan error, 1)
	go func() {
		resp, err := svc.WaitSandbox(ctx, &sandboxapi.WaitSandboxRequest{
			SandboxID: "sandbox-1",
		})
		if err != nil {
			waitErr <- err
			return
		}
		waitDone <- resp
	}()

	time.Sleep(10 * time.Millisecond)

	if _, err := svc.StopSandbox(ctx, &sandboxapi.StopSandboxRequest{
		SandboxID: "sandbox-1",
	}); err != nil {
		t.Fatalf("StopSandbox error = %v", err)
	}

	select {
	case err := <-waitErr:
		t.Fatalf("WaitSandbox error = %v", err)
	case resp := <-waitDone:
		if resp.GetExitedAt() == nil {
			t.Fatal("WaitSandbox ExitedAt = nil, want timestamp")
		}
	case <-time.After(time.Second):
		t.Fatal("WaitSandbox did not return after StopSandbox")
	}

	if _, err := svc.PingSandbox(ctx, &sandboxapi.PingRequest{
		SandboxID: "sandbox-1",
	}); err != nil {
		t.Fatalf("PingSandbox error = %v", err)
	}

	if _, err := svc.ShutdownSandbox(ctx, &sandboxapi.ShutdownSandboxRequest{
		SandboxID: "sandbox-1",
	}); err != nil {
		t.Fatalf("ShutdownSandbox error = %v", err)
	}

	select {
	case <-sd.ch:
	case <-time.After(time.Second):
		t.Fatal("shutdown service was not notified")
	}
}
