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
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/containerd/typeurl/v2"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"

	runtimeAPI "github.com/containerd/containerd/v2/api/runtime/sandbox/v1"
	"github.com/containerd/containerd/v2/api/types"
	"github.com/containerd/containerd/v2/containers"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/events"
	"github.com/containerd/containerd/v2/events/exchange"
	"github.com/containerd/containerd/v2/identifiers"
	"github.com/containerd/containerd/v2/mount"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/containerd/v2/runtime"
	v2 "github.com/containerd/containerd/v2/runtime/v2"
	"github.com/containerd/containerd/v2/sandbox"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.SandboxControllerPlugin,
		ID:   "shim",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.SandboxStorePlugin,
			plugins.ShimPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			shimPlugin, err := ic.GetByID(plugins.ShimPlugin, "shim")
			if err != nil {
				return nil, err
			}

			exchangePlugin, err := ic.GetByID(plugins.EventPlugin, "exchange")
			if err != nil {
				return nil, err
			}

			var (
				shims     = shimPlugin.(*v2.ShimManager)
				publisher = exchangePlugin.(*exchange.Exchange)
			)
			state := ic.Properties[plugins.PropertyStateDir]

			if err := os.MkdirAll(state, 0711); err != nil {
				return nil, err
			}
			c := &controllerLocal{
				state:     state,
				shims:     shims,
				publisher: publisher,
			}
			if err := c.loadExistingShims(ic.Context); err != nil {
				return nil, err
			}
			return c, nil
		},
	})
}

type controllerLocal struct {
	state     string
	shims     *v2.ShimManager
	publisher events.Publisher
}

var _ sandbox.Controller = (*controllerLocal)(nil)

func (c *controllerLocal) cleanupShim(ctx context.Context, sandboxID string, svc runtimeAPI.TTRPCSandboxService) {
	// Let the shim exit, then we can clean up the bundle after.
	if _, sErr := svc.ShutdownSandbox(ctx, &runtimeAPI.ShutdownSandboxRequest{
		SandboxID: sandboxID,
	}); sErr != nil {
		log.G(ctx).WithError(sErr).WithField("sandboxID", sandboxID).
			Error("failed to shutdown sandbox")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	dErr := c.shims.Delete(ctx, sandboxID)
	if dErr != nil {
		log.G(ctx).WithError(dErr).WithField("sandboxID", sandboxID).
			Error("failed to delete shim")
	}
}

func (c *controllerLocal) Create(ctx context.Context, info sandbox.Sandbox, opts ...sandbox.CreateOpt) (retErr error) {
	var coptions sandbox.CreateOptions
	sandboxID := info.ID
	for _, opt := range opts {
		opt(&coptions)
	}

	if _, err := c.shims.Get(ctx, sandboxID); err == nil {
		return fmt.Errorf("sandbox %s already running: %w", sandboxID, errdefs.ErrAlreadyExists)
	}

	// Generate an empty sandbox spec to start the shim process
	// need to specify the id and the annotation of SandboxID and ContainerType
	// so that "shim start" command will treat it as a sandbox and start the shim process
	spec := info.Spec
	if spec == nil || spec.GetValue() == nil {
		container := &containers.Container{ID: sandboxID}
		s, err := oci.GenerateSpec(ctx, nil, container, defaultSandboxSpecOpts(sandboxID))
		if err != nil {
			return fmt.Errorf("failed to generate spec for sandbox %q: %w", sandboxID, err)
		}
		spec, err = typeurl.MarshalAny(s)
		if err != nil {
			return fmt.Errorf("failed to marshal spec of %q to any: %w", sandboxID, err)
		}
	}

	bundle, err := c.newBundle(ctx, sandboxID, spec)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			bundle.Delete()
		}
	}()

	shim, err := c.shims.Start(ctx, sandboxID, bundle, runtime.CreateOpts{
		Spec:           spec,
		RuntimeOptions: info.Runtime.Options,
		Runtime:        info.Runtime.Name,
		TaskOptions:    nil,
	})
	if err != nil {
		return fmt.Errorf("failed to start new shim for sandbox %s: %w", sandboxID, err)
	}

	svc, err := sandbox.NewClient(shim.Client())
	if err != nil {
		c.cleanupShim(ctx, sandboxID, svc)
		return err
	}

	if _, err := svc.CreateSandbox(ctx, &runtimeAPI.CreateSandboxRequest{
		SandboxID:  sandboxID,
		BundlePath: shim.Bundle(),
		Rootfs:     mount.ToProto(coptions.Rootfs),
		Options:    protobuf.FromAny(coptions.Options),
		NetnsPath:  coptions.NetNSPath,
	}); err != nil {
		c.cleanupShim(ctx, sandboxID, svc)
		return fmt.Errorf("failed to create sandbox %s: %w", sandboxID, errdefs.FromGRPC(err))
	}

	return nil
}

func (c *controllerLocal) Start(ctx context.Context, sandboxID string) (sandbox.ControllerInstance, error) {
	shim, err := c.shims.Get(ctx, sandboxID)
	if err != nil {
		return sandbox.ControllerInstance{}, fmt.Errorf("unable to find sandbox %q", sandboxID)
	}

	svc, err := sandbox.NewClient(shim.Client())
	if err != nil {
		return sandbox.ControllerInstance{}, err
	}

	resp, err := svc.StartSandbox(ctx, &runtimeAPI.StartSandboxRequest{SandboxID: sandboxID})
	if err != nil {
		c.cleanupShim(ctx, sandboxID, svc)
		return sandbox.ControllerInstance{}, fmt.Errorf("failed to start sandbox %s: %w", sandboxID, errdefs.FromGRPC(err))
	}

	return sandbox.ControllerInstance{
		SandboxID: sandboxID,
		Pid:       resp.GetPid(),
		CreatedAt: resp.GetCreatedAt().AsTime(),
		Address:   shim.Address(),
	}, nil
}

func (c *controllerLocal) Platform(ctx context.Context, sandboxID string) (platforms.Platform, error) {
	svc, err := c.getSandbox(ctx, sandboxID)
	if err != nil {
		return platforms.Platform{}, err
	}

	response, err := svc.Platform(ctx, &runtimeAPI.PlatformRequest{SandboxID: sandboxID})
	if err != nil {
		return platforms.Platform{}, fmt.Errorf("failed to get sandbox platform: %w", errdefs.FromGRPC(err))
	}

	var platform platforms.Platform
	if p := response.GetPlatform(); p != nil {
		platform.OS = p.OS
		platform.Architecture = p.Architecture
		platform.Variant = p.Variant
	}
	return platform, nil
}

func (c *controllerLocal) Stop(ctx context.Context, sandboxID string, opts ...sandbox.StopOpt) error {
	var soptions sandbox.StopOptions
	for _, opt := range opts {
		opt(&soptions)
	}
	req := &runtimeAPI.StopSandboxRequest{SandboxID: sandboxID}
	if soptions.Timeout != nil {
		req.TimeoutSecs = uint32(soptions.Timeout.Seconds())
	}

	svc, err := c.getSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}

	if _, err := svc.StopSandbox(ctx, req); err != nil {
		return fmt.Errorf("failed to stop sandbox: %w", errdefs.FromGRPC(err))
	}

	return nil
}

func (c *controllerLocal) Shutdown(ctx context.Context, sandboxID string) error {
	svc, err := c.getSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}

	_, err = svc.ShutdownSandbox(ctx, &runtimeAPI.ShutdownSandboxRequest{SandboxID: sandboxID})
	if err != nil {
		return fmt.Errorf("failed to shutdown sandbox: %w", errdefs.FromGRPC(err))
	}

	if err := c.shims.Delete(ctx, sandboxID); err != nil {
		return fmt.Errorf("failed to delete sandbox shim: %w", err)
	}

	return nil
}

func (c *controllerLocal) Wait(ctx context.Context, sandboxID string) (sandbox.ExitStatus, error) {
	svc, err := c.getSandbox(ctx, sandboxID)
	if err != nil {
		return sandbox.ExitStatus{}, err
	}

	resp, err := svc.WaitSandbox(ctx, &runtimeAPI.WaitSandboxRequest{
		SandboxID: sandboxID,
	})

	if err != nil {
		return sandbox.ExitStatus{}, fmt.Errorf("failed to wait sandbox %s: %w", sandboxID, errdefs.FromGRPC(err))
	}

	return sandbox.ExitStatus{
		ExitStatus: resp.GetExitStatus(),
		ExitedAt:   resp.GetExitedAt().AsTime(),
	}, nil
}

func (c *controllerLocal) Status(ctx context.Context, sandboxID string, verbose bool) (sandbox.ControllerStatus, error) {
	shim, err := c.shims.Get(ctx, sandboxID)
	if err != nil {
		return sandbox.ControllerStatus{}, fmt.Errorf("unable to find shim for sandbox %q: %w", sandboxID, errdefs.ErrNotFound)
	}

	svc, err := c.getSandbox(ctx, sandboxID)
	if errdefs.IsNotFound(err) {
		return sandbox.ControllerStatus{
			SandboxID: sandboxID,
			ExitedAt:  time.Now(),
		}, nil
	}
	if err != nil {
		return sandbox.ControllerStatus{}, err
	}

	resp, err := svc.SandboxStatus(ctx, &runtimeAPI.SandboxStatusRequest{
		SandboxID: sandboxID,
		Verbose:   verbose,
	})
	if err != nil {
		return sandbox.ControllerStatus{}, fmt.Errorf("failed to query sandbox %s status: %w", sandboxID, err)
	}

	return sandbox.ControllerStatus{
		SandboxID: sandboxID,
		Pid:       resp.GetPid(),
		State:     resp.GetState(),
		Address:   shim.Address(),
		Info:      resp.GetInfo(),
		CreatedAt: resp.GetCreatedAt().AsTime(),
		ExitedAt:  resp.GetExitedAt().AsTime(),
		Extra:     resp.GetExtra(),
	}, nil
}

func (c *controllerLocal) Metrics(ctx context.Context, sandboxID string) (*types.Metric, error) {
	sb, err := c.getSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	req := &runtimeAPI.SandboxMetricsRequest{SandboxID: sandboxID}
	resp, err := sb.SandboxMetrics(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Metrics, nil
}

func (c *controllerLocal) UpdateResource(ctx context.Context, sandboxID string, req sandbox.TaskResources) error {
	return nil
}

func (c *controllerLocal) getSandbox(ctx context.Context, id string) (runtimeAPI.TTRPCSandboxService, error) {
	shim, err := c.shims.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	return sandbox.NewClient(shim.Client())
}

// newBundle returns a new bundle on disk
func (c *controllerLocal) newBundle(ctx context.Context, id string, spec typeurl.Any) (b *v2.Bundle, err error) {
	if err := identifiers.Validate(id); err != nil {
		return nil, fmt.Errorf("invalid sandbox id %s: %w", id, err)
	}

	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	b = &v2.Bundle{
		ID:        id,
		Path:      filepath.Join(c.state, ns, id),
		Namespace: ns,
	}
	var paths []string
	defer func() {
		if err != nil {
			for _, d := range paths {
				os.RemoveAll(d)
			}
		}
	}()

	// create state directory for the bundle
	if err := os.MkdirAll(filepath.Dir(b.Path), 0711); err != nil {
		return nil, err
	}
	if err := os.Mkdir(b.Path, 0700); err != nil {
		return nil, err
	}
	if spec := spec.GetValue(); spec != nil {
		// write the spec to the bundle
		specPath := filepath.Join(b.Path, oci.ConfigFilename)
		err = os.WriteFile(specPath, spec, 0666)
		if err != nil {
			return nil, fmt.Errorf("failed to write bundle spec: %w", err)
		}
	}
	paths = append(paths, b.Path)

	return b, nil
}

func defaultSandboxSpecOpts(sandboxID string) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if s.Annotations == nil {
			s.Annotations = make(map[string]string)
		}
		s.Annotations["io.kubernetes.cri.sandbox-id"] = sandboxID
		s.Annotations["io.kubernetes.cri.container-type"] = "sandbox"
		return nil
	}
}

func (c *controllerLocal) loadExistingShims(ctx context.Context) error {
	nsDirs, err := os.ReadDir(c.state)
	if err != nil {
		return err
	}
	for _, nsd := range nsDirs {
		if !nsd.IsDir() {
			continue
		}
		ns := nsd.Name()
		// skip hidden directories
		if len(ns) > 0 && ns[0] == '.' {
			continue
		}
		log.G(ctx).WithField("namespace", ns).Debug("loading sandbox shims in namespace")
		if err := c.loadShims(namespaces.WithNamespace(ctx, ns)); err != nil {
			log.G(ctx).WithField("namespace", ns).WithError(err).Error("loading sandbox shims in namespace")
			continue
		}
		if err := c.cleanupWorkDirs(namespaces.WithNamespace(ctx, ns)); err != nil {
			log.G(ctx).WithField("namespace", ns).WithError(err).Error("cleanup working directory in namespace")
			continue
		}
	}
	return nil
}

func (c *controllerLocal) loadShims(ctx context.Context) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	bundles, err := os.ReadDir(filepath.Join(c.state, ns))
	if err != nil {
		return err
	}
	for _, sd := range bundles {
		if !sd.IsDir() {
			continue
		}
		id := sd.Name()
		// skip hidden directories
		if len(id) > 0 && id[0] == '.' {
			continue
		}
		bundle := &v2.Bundle{
			ID:        id,
			Path:      filepath.Join(c.state, ns, id),
			Namespace: ns,
		}
		// fast path
		f, err := os.Open(bundle.Path)
		if err != nil {
			bundle.Delete()
			log.G(ctx).WithError(err).Errorf("fast path read bundle path for %s", bundle.Path)
			continue
		}

		bf, err := f.Readdirnames(-1)
		f.Close()
		if err != nil {
			bundle.Delete()
			log.G(ctx).WithError(err).Errorf("fast path read bundle path for %s", bundle.Path)
			continue
		}
		if len(bf) == 0 {
			bundle.Delete()
			continue
		}

		if err := c.shims.LoadShim(ctx, bundle, func() {}); err != nil {
			log.G(ctx).WithError(err).Errorf("failed to load shim %s", bundle.Path)
			bundle.Delete()
			continue
		}
	}
	return nil
}

func (c *controllerLocal) cleanupWorkDirs(ctx context.Context) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	f, err := os.Open(filepath.Join(c.state, ns))
	if err != nil {
		return err
	}
	defer f.Close()

	dirs, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}

	for _, dir := range dirs {
		// if the shim was not loaded, cleanup and empty working directory
		// this can happen on a reboot where /run for the bundle state is cleaned up
		// but that persistent working dir is left
		if _, err := c.shims.Get(ctx, dir); err != nil {
			path := filepath.Join(c.state, ns, dir)
			if err := os.RemoveAll(path); err != nil {
				log.G(ctx).WithError(err).Errorf("cleanup working dir %s", path)
			}
		}
	}
	return nil
}
