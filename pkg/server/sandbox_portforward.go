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
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"

	ctrdutil "github.com/containerd/cri/pkg/containerd/util"
	sandboxstore "github.com/containerd/cri/pkg/store/sandbox"
)

// PortForward prepares a streaming endpoint to forward ports from a PodSandbox, and returns the address.
func (c *criContainerdService) PortForward(ctx context.Context, r *runtime.PortForwardRequest) (retRes *runtime.PortForwardResponse, retErr error) {
	// TODO(random-liu): Run a socat container inside the sandbox to do portforward.
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("failed to find sandbox %q: %v", r.GetPodSandboxId(), err)
	}
	if sandbox.Status.Get().State != sandboxstore.StateReady {
		return nil, errors.New("sandbox container is not running")
	}
	// TODO(random-liu): Verify that ports are exposed.
	return c.streamServer.GetPortForward(r)
}

// portForward requires `nsenter` and `socat` on the node, it uses `nsenter` to enter the
// sandbox namespace, and run `socat` inside the namespace to forward stream for a specific
// port. The `socat` command keeps running until it exits or client disconnect.
func (c *criContainerdService) portForward(id string, port int32, stream io.ReadWriteCloser) error {
	s, err := c.sandboxStore.Get(id)
	if err != nil {
		return fmt.Errorf("failed to find sandbox %q in store: %v", id, err)
	}
	t, err := s.Container.Task(ctrdutil.NamespacedContext(), nil)
	if err != nil {
		return fmt.Errorf("failed to get sandbox container task: %v", err)
	}
	pid := t.Pid()

	socat, err := exec.LookPath("socat")
	if err != nil {
		return fmt.Errorf("failed to find socat: %v", err)
	}

	// Check following links for meaning of the options:
	// * socat: https://linux.die.net/man/1/socat
	// * nsenter: http://man7.org/linux/man-pages/man1/nsenter.1.html
	args := []string{"-t", fmt.Sprintf("%d", pid), "-n", socat,
		"-", fmt.Sprintf("TCP4:localhost:%d", port)}

	nsenter, err := exec.LookPath("nsenter")
	if err != nil {
		return fmt.Errorf("failed to find nsenter: %v", err)
	}

	logrus.Infof("Executing port forwarding command: %s %s", nsenter, strings.Join(args, " "))

	cmd := exec.Command(nsenter, args...)
	cmd.Stdout = stream

	stderr := new(bytes.Buffer)
	cmd.Stderr = stderr

	// If we use Stdin, command.Run() won't return until the goroutine that's copying
	// from stream finishes. Unfortunately, if you have a client like telnet connected
	// via port forwarding, as long as the user's telnet client is connected to the user's
	// local listener that port forwarding sets up, the telnet session never exits. This
	// means that even if socat has finished running, command.Run() won't ever return
	// (because the client still has the connection and stream open).
	//
	// The work around is to use StdinPipe(), as Wait() (called by Run()) closes the pipe
	// when the command (socat) exits.
	in, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %v", err)
	}
	go func() {
		if _, err := io.Copy(in, stream); err != nil {
			logrus.WithError(err).Errorf("Failed to copy port forward input for %q port %d", id, port)
		}
		in.Close()
		logrus.Debugf("Finish copy port forward input for %q port %d: %v", id, port)
	}()

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nsenter command returns error: %v, stderr: %q", err, stderr.String())
	}

	logrus.Infof("Finish port forwarding for %q port %d", id, port)

	return nil
}
