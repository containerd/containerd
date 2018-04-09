/*
Copyright 2017 The Kubernetes Authors.

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

package server

import (
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"

	sandboxstore "github.com/containerd/cri/pkg/store/sandbox"
)

// PortForward prepares a streaming endpoint to forward ports from a PodSandbox, and returns the address.
func (c *criService) PortForward(ctx context.Context, r *runtime.PortForwardRequest) (retRes *runtime.PortForwardResponse, retErr error) {
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find sandbox %q", r.GetPodSandboxId())
	}
	if sandbox.Status.Get().State != sandboxstore.StateReady {
		return nil, errors.New("sandbox container is not running")
	}
	// TODO(random-liu): Verify that ports are exposed.
	return c.streamServer.GetPortForward(r)
}

// portForward requires it uses netns to enter the sandbox namespace,
// and forward stream for a specific port.
func (c *criService) portForward(id string, port int32, stream io.ReadWriteCloser) error {
	s, err := c.sandboxStore.Get(id)
	if err != nil {
		return errors.Wrapf(err, "failed to find sandbox %q in store", id)
	}
	if s.NetNS == nil || s.NetNS.Closed() {
		return errors.Errorf("network namespace for sandbox %q is closed", id)
	}

	err = s.NetNS.GetNs().Do(func(_ ns.NetNS) error {
		var wg sync.WaitGroup
		client, err := net.Dial("tcp4", fmt.Sprintf("localhost:%d", port))
		if err != nil {
			return errors.Wrapf(err, "failed to dial %q", port)
		}
		wg.Add(1)
		go func() {
			defer client.Close()
			if _, err := io.Copy(client, stream); err != nil {
				logrus.WithError(err).Errorf("Failed to copy port forward input for %q port %d", id, port)
			}
			logrus.Infof("Finish copy port forward input for %q port %d", id, port)
			wg.Done()
		}()
		wg.Add(1)
		go func() {
			defer stream.Close()
			if _, err := io.Copy(stream, client); err != nil {
				logrus.WithError(err).Errorf("Failed to copy port forward output for %q port %d", id, port)
			}
			logrus.Infof("Finish copy port forward output for %q port %d", id, port)
			wg.Done()
		}()
		wg.Wait()

		return nil
	})
	if err != nil {
		return errors.Wrapf(err, "failed to execute portforward in network namespace %s", s.NetNS.GetPath())
	}
	logrus.Infof("Finish port forwarding for %q port %d", id, port)

	return nil
}
