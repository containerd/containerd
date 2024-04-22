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

package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/containerd/log"
)

func (c *criService) portForward(ctx context.Context, id string, port int32, stream io.ReadWriter) error {
	sandbox, err := c.sandboxStore.Get(id)
	if err != nil {
		return fmt.Errorf("failed to find sandbox %q in store: %w", id, err)
	}

	var podIP string
	if !hostNetwork(sandbox.Config) {
		// get ip address of the sandbox
		podIP, _, err = c.getIPs(sandbox)
		if err != nil {
			return fmt.Errorf("failed to get sandbox ip: %w", err)
		}
	} else {
		// HPCs use the host networking namespace.
		// Therefore, dial to localhost.
		podIP = "localhost"
	}

	err = func() error {
		var conn net.Conn
		conn, err = net.Dial("tcp", net.JoinHostPort(podIP, fmt.Sprintf("%d", port)))
		if err != nil {
			return fmt.Errorf("failed to connect to %s:%d for pod %q: %v", podIP, port, id, err)
		}
		log.G(ctx).Debugf("Connection to ip %s and port %d was successful", podIP, port)

		defer conn.Close()

		// copy stream
		errCh := make(chan error, 2)
		// Copy from the namespace port connection to the client stream
		go func() {
			log.G(ctx).Debugf("PortForward copying data from namespace %q port %d to the client stream", id, port)
			_, err := io.Copy(stream, conn)
			errCh <- err
		}()

		// Copy from the client stream to the namespace port connection
		go func() {
			log.G(ctx).Debugf("PortForward copying data from client stream to namespace %q port %d", id, port)
			_, err := io.Copy(conn, stream)
			errCh <- err
		}()

		// Wait until the first error is returned by one of the connections
		// we use errFwd to store the result of the port forwarding operation
		// if the context is cancelled close everything and return
		var errFwd error
		select {
		case errFwd = <-errCh:
			log.G(ctx).Debugf("PortForward stop forwarding in one direction in network namespace %q port %d: %v", id, port, errFwd)
		case <-ctx.Done():
			log.G(ctx).Debugf("PortForward cancelled in network namespace %q port %d: %v", id, port, ctx.Err())
			return ctx.Err()
		}
		// give a chance to terminate gracefully or timeout
		// after 1s
		const timeout = time.Second
		select {
		case e := <-errCh:
			if errFwd == nil {
				errFwd = e
			}
			log.G(ctx).Debugf("PortForward stopped forwarding in both directions in network namespace %q port %d: %v", id, port, e)
		case <-time.After(timeout):
			log.G(ctx).Debugf("PortForward timed out waiting to close the connection in network namespace %q port %d", id, port)
		case <-ctx.Done():
			log.G(ctx).Debugf("PortForward cancelled in network namespace %q port %d: %v", id, port, ctx.Err())
			errFwd = ctx.Err()
		}

		return errFwd
	}()

	if err != nil {
		return fmt.Errorf("failed to execute portforward for podId %v, podIp %v, err: %w", id, podIP, err)
	}
	log.G(ctx).Debugf("Finish port forwarding for windows %q port %d", id, port)

	return nil
}
