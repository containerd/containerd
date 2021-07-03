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
	"io/ioutil"
	"net"
	"os"
	"time"

	"github.com/containerd/containerd/log"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/containerd/containerd/runtime/v2/services/portforward"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

// tmpUDS generates a temporary UDS addr.
func tmpUDSAddr() (string, error) {
	tmpFile, err := ioutil.TempFile("", "cri-portforward")
	if err != nil {
		return "", err
	}
	path := tmpFile.Name()
	// Remove the tempfile and just use it's name.
	os.Remove(path)

	return path, nil
}

// portForward initiates a port forwarding request and forwards data over the stream.
func (c *criService) portForward(ctx context.Context, id string, port int32, stream io.ReadWriteCloser) error {
	// Attempt to start port forwarding via the port forwarding client.
	// NOTE: Do not use volitile root dir as it amy exceed max length for UDS addr.
	addr, err := tmpUDSAddr()
	if err != nil {
		return errors.Wrap(err, "failed to get temp uds address")
	}

	l, err := net.Listen("unix", addr)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to listen on %s", addr))
	}
	defer l.Close()

	if err := c.pfClient.PortForward(ctx, portforward.PortForwardOpts{
		ID:   id,
		Port: port,
		Addr: addr,
	}); err != nil {
		// If port forwarding via the shim is not supported, fall back to local
		// port forwarding via the network namespace.
		s, err := c.sandboxStore.Get(id)
		if err != nil {
			return errors.Wrapf(err, "failed to find sandbox %q in store", id)
		}
		return c.localPortForward(ctx, s, id, port, stream)
	}

	// Runtimes should create a single connection on the UDS.
	connCh := make(chan net.Conn)
	connErrCh := make(chan error)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			connErrCh <- err
			return
		}
		connCh <- conn
	}()

	var conn net.Conn
	select {
	case conn = <-connCh:
		defer conn.Close()
	case err := <-connErrCh:
		return err
	case <-time.After(10 * time.Second):
		return fmt.Errorf("port forwarding timeout for sandbox %q", id)
	}

	// Copy data to/from the UDS.
	errCh := make(chan error, 2)
	// Copy from the the namespace port connection to the client stream
	go func() {
		log.G(ctx).Debugf("PortForward copying data from container %q port %d to the client stream", id, port)
		_, err := io.Copy(stream, conn)
		errCh <- err
	}()

	// Copy from the client stream to the namespace port connection
	go func() {
		log.G(ctx).Debugf("PortForward copying data from client stream to container %q port %d", id, port)
		_, err := io.Copy(conn, stream)
		errCh <- err
	}()

	// Wait until the first error is returned by one of the connections
	// we use errFwd to store the result of the port forwarding operation
	// if the context is cancelled close everything and return
	var errFwd error
	select {
	case errFwd = <-errCh:
		log.G(ctx).Debugf("PortForward stop forwarding in one direction %q port %d: %v", id, port, errFwd)
	case <-ctx.Done():
		log.G(ctx).Debugf("PortForward cancelled in network container %q port %d: %v", id, port, ctx.Err())
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
		log.G(ctx).Debugf("PortForward stopped forwarding in both directions in container %q port %d: %v", id, port, e)
	case <-time.After(timeout):
		log.G(ctx).Debugf("PortForward timed out waiting to close the connection in container %q port %d", id, port)
	case <-ctx.Done():
		log.G(ctx).Debugf("PortForward cancelled in container %q port %d: %v", id, port, ctx.Err())
		errFwd = ctx.Err()
	}

	return errFwd
}

func (c *criService) localPortForward(ctx context.Context, s sandboxstore.Sandbox, id string, port int32, stream io.ReadWriteCloser) error {
	var netNSDo func(func(ns.NetNS) error) error
	// netNSPath is the network namespace path for logging.
	var netNSPath string
	securityContext := s.Config.GetLinux().GetSecurityContext()
	hostNet := securityContext.GetNamespaceOptions().GetNetwork() == runtime.NamespaceMode_NODE
	if !hostNet {
		if closed, err := s.NetNS.Closed(); err != nil {
			return errors.Wrapf(err, "failed to check network namespace closed for sandbox %q", id)
		} else if closed {
			return errors.Errorf("network namespace for sandbox %q is closed", id)
		}
		netNSDo = s.NetNS.Do
		netNSPath = s.NetNS.GetPath()
	} else {
		// Run the function directly for host network.
		netNSDo = func(do func(_ ns.NetNS) error) error {
			return do(nil)
		}
		netNSPath = "host"
	}

	log.G(ctx).Infof("Executing port forwarding in network namespace %q", netNSPath)
	if err := netNSDo(func(_ ns.NetNS) error {
		defer stream.Close()
		// localhost can resolve to both IPv4 and IPv6 addresses in dual-stack systems
		// but the application can be listening in one of the IP families only.
		// golang has enabled RFC 6555 Fast Fallback (aka HappyEyeballs) by default in 1.12
		// It means that if a host resolves to both IPv6 and IPv4, it will try to connect to any
		// of those addresses and use the working connection.
		// However, the implementation uses go routines to start both connections in parallel,
		// and this cases that the connection is done outside the namespace, so we try to connect
		// serially.
		// We try IPv4 first to keep current behavior and we fallback to IPv6 if the connection fails.
		// xref https://github.com/golang/go/issues/44922
		var conn net.Conn
		conn, err := net.Dial("tcp4", fmt.Sprintf("localhost:%d", port))
		if err != nil {
			var errV6 error
			conn, errV6 = net.Dial("tcp6", fmt.Sprintf("localhost:%d", port))
			if errV6 != nil {
				return fmt.Errorf("failed to connect to localhost:%d inside namespace %q, IPv4: %v IPv6 %v ", port, id, err, errV6)
			}
		}
		defer conn.Close()

		errCh := make(chan error, 2)
		// Copy from the the namespace port connection to the client stream
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
	}); err != nil {
		return errors.Wrapf(err, "failed to execute portforward in network namespace %q", netNSPath)
	}
	log.G(ctx).Infof("Finish port forwarding for %q port %d", id, port)

	return nil
}
