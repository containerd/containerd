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
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/containerd/log"
	"github.com/containernetworking/plugins/pkg/ns"
)

// portForward uses netns to enter the sandbox namespace, and forwards a stream inside the
// namespace to a specific port. It keeps forwarding until it exits or client disconnect.
func (c *criService) portForward(ctx context.Context, id string, port int32, stream io.ReadWriteCloser) error {
	s, err := c.sandboxStore.Get(id)
	if err != nil {
		return fmt.Errorf("failed to find sandbox %q in store: %w", id, err)
	}

	var (
		netNSDo func(func(ns.NetNS) error) error
		// netNSPath is the network namespace path for logging.
		netNSPath string
	)
	if !hostNetwork(s.Config) {
		if closed, err := s.NetNS.Closed(); err != nil {
			return fmt.Errorf("failed to check netwok namespace closed for sandbox %q: %w", id, err)
		} else if closed {
			return fmt.Errorf("network namespace for sandbox %q is closed", id)
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

	// podIPs are used as fallback dial targets below, for workloads that
	// don't listen on loopback (e.g. bind only to a pod IP, or run inside
	// a VM under Kata/other VM-isolated runtimes). getIPs can return more
	// than one IP for dual-stack sandboxes or additional pod networks.
	podIP, additionalPodIPs, err := c.getIPs(s)
	if err != nil {
		return fmt.Errorf("failed to get sandbox ip for %q: %w", id, err)
	}
	var podIPs []string
	if podIP != "" {
		podIPs = append(podIPs, podIP)
	}
	podIPs = append(podIPs, additionalPodIPs...)

	// The sandbox's own loopback is never reachable from this, host-side,
	// network namespace for VM-isolated runtimes, so skip straight to the
	// pod IP fallback for those instead of dialing localhost first.
	ociRuntime, err := c.config.GetSandboxRuntime(s.Config, s.Metadata.RuntimeHandler)
	if err != nil {
		return fmt.Errorf("failed to get sandbox runtime for %q: %w", id, err)
	}
	skipLocalhost := len(podIPs) > 0 && isVMBasedRuntime(ociRuntime.Type)

	log.G(ctx).Infof("Executing port forwarding in network namespace %q", netNSPath)
	var conn net.Conn
	if !skipLocalhost {
		err = netNSDo(func(_ ns.NetNS) error {
			var dialErr error
			conn, dialErr = dialLocalhost(ctx, port)
			return dialErr
		})
	} else {
		err = fmt.Errorf("skipped for VM-isolated runtime %q", ociRuntime.Type)
	}
	if err != nil && len(podIPs) > 0 {
		// localhost isn't reachable from inside the sandbox's own netns, either
		// because the workload only binds a pod IP, or because it runs in a
		// VM (Kata) whose loopback isn't reachable at all from this host-side
		// namespace. Dial the pod IPs from containerd's own (host root) network
		// namespace instead: this is the same path kubelet's liveness/readiness
		// probes use to reach a pod, so any CNI setup where those work will
		// route this the same way. For VM-isolated runtimes, this traffic
		// genuinely arrives on the sandbox's veth, which is what a tc-based
		// redirect (e.g. Kata's tcfilter network model) forwards into the VM;
		// a dial from inside the netns cannot do this, since a locally
		// destined connection is delivered via the kernel's loopback shortcut
		// and never traverses the veth's ingress path.
		log.G(ctx).Debugf("localhost unreachable for sandbox %q port %d (%v), falling back to pod IPs %v from host netns", id, port, err, podIPs)
		conn, err = dialPodIPs(ctx, podIPs, port)
	}
	if err != nil {
		return fmt.Errorf("failed to execute portforward in network namespace %q: %w", netNSPath, err)
	}

	err = func() error {
		defer stream.Close()
		defer conn.Close()

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
		// https://linux.die.net/man/1/socat
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
		return fmt.Errorf("failed to execute portforward for %q port %d: %w", id, port, err)
	}
	log.G(ctx).Infof("Finish port forwarding for %q port %d", id, port)

	return nil
}

// dialTimeout bounds each individual connect attempt below, so a
// silently-dropped connection can't hang the whole request.
const dialTimeout = 5 * time.Second

// dialPodIPs tries each of ips in order, returning the first successful
// connection to ip:port. Each ip is a literal (never a hostname), so its
// family is unambiguous and a single "tcp" dial suffices for both IPv4 and IPv6.
func dialPodIPs(ctx context.Context, ips []string, port int32) (net.Conn, error) {
	d := net.Dialer{Timeout: dialTimeout}
	var errs error
	tried := 0
	for _, ip := range ips {
		if ip == "" {
			continue
		}
		tried++
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, fmt.Sprintf("%d", port)))
		if err == nil {
			return conn, nil
		}
		errs = errors.Join(errs, err)
	}
	if tried == 0 {
		return nil, fmt.Errorf("no pod IPs provided")
	}
	return nil, errs
}

// dialLocalhost connects to localhost:port. It must be called from inside the
// sandbox network namespace (see ns.NetNS.Do).
//
// localhost can resolve to both IPv4 and IPv6 addresses in dual-stack systems,
// but the application can be listening in one of the IP families only. golang
// has enabled RFC 6555 Fast Fallback (aka HappyEyeballs) by default in 1.12.
// It means that if a host resolves to both IPv6 and IPv4, it will try to
// connect to any of those addresses and use the working connection. However,
// the implementation uses goroutines to start both connections in parallel,
// and this causes the connection to be dialed outside the namespace, so we
// try to connect serially. We try IPv4 first to keep current behavior and we
// fallback to IPv6 if the connection fails.
// xref https://github.com/golang/go/issues/44922
func dialLocalhost(ctx context.Context, port int32) (net.Conn, error) {
	d := net.Dialer{Timeout: dialTimeout}
	conn, errV4 := d.DialContext(ctx, "tcp4", fmt.Sprintf("localhost:%d", port))
	if errV4 == nil {
		return conn, nil
	}
	conn, errV6 := d.DialContext(ctx, "tcp6", fmt.Sprintf("localhost:%d", port))
	if errV6 == nil {
		return conn, nil
	}
	return nil, fmt.Errorf("IPv4: %v IPv6: %v", errV4, errV6)
}
