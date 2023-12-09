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

package pause

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/sys/reaper"
	"github.com/containerd/go-runc"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"
	"golang.org/x/sys/unix"
	criapi "k8s.io/cri-api/pkg/apis/runtime/v1"

	api "github.com/containerd/containerd/v2/api/runtime/sandbox/v1"
	"github.com/containerd/containerd/v2/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/api/types"
	runc2 "github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/runc"
	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/containerd/v2/pkg/shutdown"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/containerd/v2/runtime/v2/shim"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.TTRPCPlugin,
		ID:   "pause",
		Requires: []plugin.Type{
			plugins.InternalPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ss, err := ic.GetByID(plugins.InternalPlugin, "shutdown")
			if err != nil {
				return nil, err
			}
			ps := &pauseService{
				shutdown: ss.(shutdown.Service),
			}
			ec := reaper.Default.Subscribe()
			go func() {
				for e := range ec {
					if e.Pid == ps.pid {
						p, _ := ps.container.Process("")
						p.SetExited(e.Status)
					}
				}
			}()
			return ps, nil
		},
	})
}

var (
	_ = shim.TTRPCService(&pauseService{})
	_ = api.TTRPCSandboxService(&pauseService{})
)

// pauseService is an extension for task v2 runtime to support Pod "pause" containers via sandbox API.
type pauseService struct {
	container *runc2.Container
	pid       int
	createAt  time.Time
	shutdown  shutdown.Service
}

func (p *pauseService) RegisterTTRPC(server *ttrpc.Server) error {
	api.RegisterTTRPCSandboxService(server, p)
	return nil
}

func (p *pauseService) CreateSandbox(ctx context.Context, req *api.CreateSandboxRequest) (*api.CreateSandboxResponse, error) {
	log.G(ctx).Debugf("create sandbox request: %+v", req)
	opts, err := typeurl.MarshalAny(req.Options)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal options to any: %w", err)
	}
	var podSandboxConfig criapi.PodSandboxConfig
	if err := typeurl.UnmarshalTo(opts, &podSandboxConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal options to PodSandboxConfig: %w", err)
	}
	spec, err := pauseContainerSpec(req.SandboxID, &podSandboxConfig, req.NetnsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to generate pause container spec: %w", err)
	}
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pause container spec: %w", err)
	}

	// write the new spec to the bundle
	specPath := filepath.Join(req.BundlePath, oci.ConfigFilename)
	err = os.WriteFile(specPath, specBytes, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to write bundle spec: %w", err)
	}

	rootfs := filepath.Join(req.BundlePath, "rootfs")
	if err := os.MkdirAll(rootfs, 0711); err != nil {
		return nil, err
	}
	sandboxRootfs := filepath.Join(req.BundlePath, "sandbox_rootfs")
	if err := os.MkdirAll(sandboxRootfs, 0711); err != nil {
		return nil, err
	}

	// For simplicity, we assume that there is a pause file in /use/bin,
	// and we copy it to the "rootfs" in bundle
	pauseFile, err := os.Open("/usr/bin/pause")
	if err != nil {
		return nil, fmt.Errorf("failed to open pause executable: %w", err)
	}
	sandboxPauseFile := filepath.Join(sandboxRootfs, "pause")
	dest, err := os.OpenFile(sandboxPauseFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0500)
	if err != nil {
		return nil, fmt.Errorf("failed to create pause executable in rootfs: %w", err)
	}
	if _, err := io.Copy(dest, pauseFile); err != nil {
		if err != nil {
			return nil, fmt.Errorf("failed to copy pause executable to rootfs: %w", err)
		}
	}
	pauseFile.Close()
	dest.Close()

	if podSandboxConfig.Linux.SecurityContext.RunAsUser != nil {
		err = os.Chown(sandboxPauseFile, int(podSandboxConfig.Linux.SecurityContext.RunAsUser.GetValue()), -1)
		if err != nil {
			return nil, fmt.Errorf("failed to chown of the pause executable: %w", err)
		}
	}

	if podSandboxConfig.Linux.SecurityContext.RunAsGroup != nil {
		err = os.Chown(sandboxPauseFile, -1, int(podSandboxConfig.Linux.SecurityContext.RunAsGroup.GetValue()))
		if err != nil {
			return nil, fmt.Errorf("failed to chown of the pause executable: %w", err)
		}
	}

	if err := syscall.Mount(sandboxRootfs, rootfs, "bind", unix.MS_RDONLY|unix.MS_REC|unix.MS_BIND, ""); err != nil {
		return nil, fmt.Errorf("failed to bind mount the pause executable: %w", err)
	}

	platform, err := runc2.NewPlatform()
	if err != nil {
		return nil, fmt.Errorf("create namespace: %w", err)
	}
	c, err := runc2.NewContainer(ctx, platform, &task.CreateTaskRequest{
		ID:     req.SandboxID,
		Bundle: req.BundlePath,
	})
	p.container = c
	p.pid = c.Pid()
	p.createAt = time.Now()
	return &api.CreateSandboxResponse{}, nil
}

func (p *pauseService) StartSandbox(ctx context.Context, req *api.StartSandboxRequest) (*api.StartSandboxResponse, error) {
	log.G(ctx).Debugf("start sandbox request: %+v", req)
	if _, err := p.container.Start(ctx, &task.StartRequest{
		ID: req.SandboxID,
	}); err != nil {
		return nil, fmt.Errorf("failed to call runc start: %w", err)
	}
	return &api.StartSandboxResponse{
		Pid:       uint32(p.pid),
		CreatedAt: protobuf.ToTimestamp(p.createAt),
	}, nil
}

func (p *pauseService) Platform(ctx context.Context, req *api.PlatformRequest) (*api.PlatformResponse, error) {
	log.G(ctx).Debugf("platform request: %+v", req)

	platform := types.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
	}

	return &api.PlatformResponse{Platform: &platform}, nil
}

func (p *pauseService) StopSandbox(ctx context.Context, req *api.StopSandboxRequest) (*api.StopSandboxResponse, error) {
	log.G(ctx).Debugf("stop sandbox request: %+v", req)
	err := p.container.Kill(ctx, &task.KillRequest{
		ID:     req.SandboxID,
		ExecID: "",
		Signal: uint32(syscall.SIGKILL),
		All:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to stop sandbox: %w", err)
	}
	return &api.StopSandboxResponse{}, nil
}

func (p *pauseService) WaitSandbox(ctx context.Context, req *api.WaitSandboxRequest) (*api.WaitSandboxResponse, error) {
	log.G(ctx).Debugf("wait sandbox request: %+v", req)
	<-p.shutdown.Done()
	return &api.WaitSandboxResponse{
		ExitStatus: 0,
	}, nil
}

func (p *pauseService) SandboxStatus(ctx context.Context, req *api.SandboxStatusRequest) (*api.SandboxStatusResponse, error) {
	log.G(ctx).Debugf("sandbox status request: %+v", req)
	return &api.SandboxStatusResponse{
		Pid:       uint32(p.pid),
		CreatedAt: protobuf.ToTimestamp(p.createAt),
		State:     "SANDBOX_READY",
	}, nil
}

func (p *pauseService) PingSandbox(ctx context.Context, req *api.PingRequest) (*api.PingResponse, error) {
	return &api.PingResponse{}, nil
}

func (p *pauseService) ShutdownSandbox(ctx context.Context, req *api.ShutdownSandboxRequest) (*api.ShutdownSandboxResponse, error) {
	log.G(ctx).Debugf("shutdown sandbox request: %+v", req)
	if p.container != nil {
		err := p.container.Kill(ctx, &task.KillRequest{
			ID:     req.SandboxID,
			ExecID: "",
			Signal: uint32(syscall.SIGKILL),
			All:    true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to stop sandbox: %w", err)
		}
		process, _ := p.container.Process("")
		process.Wait()
		if _, err := p.container.Delete(ctx, &task.DeleteRequest{
			ID: req.SandboxID,
		}); err != nil {
			return nil, fmt.Errorf("failed to delete sandbox container by runc: %w", err)
		}
	}

	p.shutdown.Shutdown()
	return &api.ShutdownSandboxResponse{}, nil
}

func (p *pauseService) SandboxMetrics(ctx context.Context, req *api.SandboxMetricsRequest) (*api.SandboxMetricsResponse, error) {
	log.G(ctx).Debugf("sandbox metrics request: %+v", req)
	return &api.SandboxMetricsResponse{}, nil
}

func runtimeError(rErr error, msg string, runtime *runc.Runc) error {
	if rErr == nil {
		return nil
	}

	rMsg, err := getLastRuntimeError(runtime)
	switch {
	case err != nil:
		return fmt.Errorf("%s: %s (%s): %w", msg, "unable to retrieve OCI runtime error", err.Error(), rErr)
	case rMsg == "":
		return fmt.Errorf("%s: %w", msg, rErr)
	default:
		return fmt.Errorf("%s: %s", msg, rMsg)
	}
}

func getLastRuntimeError(r *runc.Runc) (string, error) {
	if r.Log == "" {
		return "", nil
	}

	f, err := os.OpenFile(r.Log, os.O_RDONLY, 0400)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var (
		errMsg string
		log    struct {
			Level string
			Msg   string
			Time  time.Time
		}
	)

	dec := json.NewDecoder(f)
	for err = nil; err == nil; {
		if err = dec.Decode(&log); err != nil && err != io.EOF {
			return "", err
		}
		if log.Level == "error" {
			errMsg = strings.TrimSpace(log.Msg)
		}
	}

	return errMsg, nil
}
