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
	"fmt"
	"io"
	"os/exec"
	"strings"

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

// portForward requires `socat` on the node. It runs `socat` to forward stream for a
// specific port. The `socat` command keeps running until it exits or client disconnect.
func (c *criService) portForward(id string, port int32, stream io.ReadWriteCloser) error {
	s, err := c.sandboxStore.Get(id)
	if err != nil {
		return errors.Wrapf(err, "failed to find sandbox %q in store", id)
	}
	if s.Status.Get().State != sandboxstore.StateReady {
		return errors.Wrap(err, "sandbox container is not running")
	}
	// We use pod ip instead of netns.Do + localhost. One reason is that
	// for sandbox container e.g. gvisor and kata, the network stack is
	// inside sandbox, netns.Do + localhost will not work. However, accessing
	// pod ip from host should always work, because it is an assumption of
	// Kubernetes network model. (Related issue containerd/cri-containerd#524)
	var addr string
	securityContext := s.Config.GetLinux().GetSecurityContext()
	hostNet := securityContext.GetNamespaceOptions().GetNetwork() == runtime.NamespaceMode_NODE
	if !hostNet {
		addr = s.IP
	} else {
		addr = "localhost"
	}

	socat, err := exec.LookPath("socat")
	if err != nil {
		return errors.Wrap(err, "failed to find socat")
	}

	// Check https://linux.die.net/man/1/socat for meaning of the options.
	args := []string{socat, "-", fmt.Sprintf("TCP4:%s:%d", addr, port)}

	logrus.Infof("Executing port forwarding command %q", strings.Join(args, " "))
	cmd := exec.Command(args[0], args[1:]...)
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
		return errors.Wrap(err, "failed to create stdin pipe")
	}
	go func() {
		if _, err := io.Copy(in, stream); err != nil {
			logrus.WithError(err).Errorf("Failed to copy port forward input for %q port %d", id, port)
		}
		in.Close()
		logrus.Debugf("Finish copying port forward input for %q port %d", id, port)
	}()

	if err := cmd.Run(); err != nil {
		return errors.Errorf("socat command returns error: %v, stderr: %q", err, stderr.String())
	}
	logrus.Infof("Finish port forwarding for %q port %d", id, port)
	return nil
}
