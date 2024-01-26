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
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"time"

	k8snet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/remotecommand"
	k8scert "k8s.io/client-go/util/cert"
	"k8s.io/utils/exec"

	ctrdutil "github.com/containerd/containerd/v2/pkg/cri/util"
	"k8s.io/kubelet/pkg/cri/streaming"
)

type streamListenerMode int

const (
	x509KeyPairTLS streamListenerMode = iota
	selfSignTLS
	withoutTLS
)

func getStreamListenerMode(c *criService) (streamListenerMode, error) {
	if c.config.EnableTLSStreaming {
		if c.config.X509KeyPairStreaming.TLSCertFile != "" && c.config.X509KeyPairStreaming.TLSKeyFile != "" {
			return x509KeyPairTLS, nil
		}
		if c.config.X509KeyPairStreaming.TLSCertFile != "" && c.config.X509KeyPairStreaming.TLSKeyFile == "" {
			return -1, errors.New("must set X509KeyPairStreaming.TLSKeyFile")
		}
		if c.config.X509KeyPairStreaming.TLSCertFile == "" && c.config.X509KeyPairStreaming.TLSKeyFile != "" {
			return -1, errors.New("must set X509KeyPairStreaming.TLSCertFile")
		}
		return selfSignTLS, nil
	}
	if c.config.X509KeyPairStreaming.TLSCertFile != "" {
		return -1, errors.New("X509KeyPairStreaming.TLSCertFile is set but EnableTLSStreaming is not set")
	}
	if c.config.X509KeyPairStreaming.TLSKeyFile != "" {
		return -1, errors.New("X509KeyPairStreaming.TLSKeyFile is set but EnableTLSStreaming is not set")
	}
	return withoutTLS, nil
}

func newStreamServer(c *criService, addr, port, streamIdleTimeout string) (streaming.Server, error) {
	if addr == "" {
		a, err := k8snet.ResolveBindAddress(nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get stream server address: %w", err)
		}
		addr = a.String()
	}
	config := streaming.DefaultConfig
	if streamIdleTimeout != "" {
		var err error
		config.StreamIdleTimeout, err = time.ParseDuration(streamIdleTimeout)
		if err != nil {
			return nil, fmt.Errorf("invalid stream idle timeout: %w", err)
		}
	}
	config.Addr = net.JoinHostPort(addr, port)
	run := newStreamRuntime(c)
	tlsMode, err := getStreamListenerMode(c)
	if err != nil {
		return nil, fmt.Errorf("invalid stream server configuration: %w", err)
	}
	switch tlsMode {
	case x509KeyPairTLS:
		tlsCert, err := tls.LoadX509KeyPair(c.config.X509KeyPairStreaming.TLSCertFile, c.config.X509KeyPairStreaming.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load x509 key pair for stream server: %w", err)
		}
		config.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		}
		return streaming.NewServer(config, run)
	case selfSignTLS:
		tlsCert, err := newTLSCert()
		if err != nil {
			return nil, fmt.Errorf("failed to generate tls certificate for stream server: %w", err)
		}
		config.TLSConfig = &tls.Config{
			Certificates:       []tls.Certificate{tlsCert},
			InsecureSkipVerify: true,
		}
		return streaming.NewServer(config, run)
	case withoutTLS:
		return streaming.NewServer(config, run)
	default:
		return nil, errors.New("invalid configuration for the stream listener")
	}
}

type streamRuntime struct {
	c *criService
}

func newStreamRuntime(c *criService) streaming.Runtime {
	return &streamRuntime{c: c}
}

// Exec executes a command inside the container. exec.ExitError is returned if the command
// returns non-zero exit code.
func (s *streamRuntime) Exec(ctx context.Context, containerID string, cmd []string, stdin io.Reader, stdout, stderr io.WriteCloser,
	tty bool, resize <-chan remotecommand.TerminalSize) error {
	exitCode, err := s.c.execInContainer(ctrdutil.WithNamespace(ctx), containerID, execOptions{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
		tty:    tty,
		resize: resize,
	})
	if err != nil {
		return fmt.Errorf("failed to exec in container: %w", err)
	}
	if *exitCode == 0 {
		return nil
	}
	return &exec.CodeExitError{
		Err:  fmt.Errorf("error executing command %v, exit code %d", cmd, *exitCode),
		Code: int(*exitCode),
	}
}

func (s *streamRuntime) Attach(ctx context.Context, containerID string, in io.Reader, out, err io.WriteCloser, tty bool,
	resize <-chan remotecommand.TerminalSize) error {
	return s.c.attachContainer(ctrdutil.WithNamespace(ctx), containerID, in, out, err, tty, resize)
}

func (s *streamRuntime) PortForward(ctx context.Context, podSandboxID string, port int32, stream io.ReadWriteCloser) error {
	if port <= 0 || port > math.MaxUint16 {
		return fmt.Errorf("invalid port %d", port)
	}
	ctx = ctrdutil.WithNamespace(ctx)
	return s.c.portForward(ctx, podSandboxID, port, stream)
}

// handleResizing spawns a goroutine that processes the resize channel, calling resizeFunc for each
// remotecommand.TerminalSize received from the channel.
func handleResizing(ctx context.Context, resize <-chan remotecommand.TerminalSize, resizeFunc func(size remotecommand.TerminalSize)) {
	if resize == nil {
		return
	}

	go func() {
		defer runtime.HandleCrash()

		for {
			select {
			case <-ctx.Done():
				return
			case size, ok := <-resize:
				if !ok {
					return
				}
				if size.Height < 1 || size.Width < 1 {
					continue
				}
				resizeFunc(size)
			}
		}
	}()
}

// newTLSCert returns a self CA signed tls.certificate.
// TODO (mikebrow): replace / rewrite this function to support using CA
// signing of the certificate. Requires a security plan for kubernetes regarding
// CRI connections / streaming, etc. For example, kubernetes could configure or
// require a CA service and pass a configuration down through CRI.
func newTLSCert() (tls.Certificate, error) {
	fail := func(err error) (tls.Certificate, error) { return tls.Certificate{}, err }

	hostName, err := os.Hostname()
	if err != nil {
		return fail(fmt.Errorf("failed to get hostname: %w", err))
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return fail(fmt.Errorf("failed to get host IP addresses: %w", err))
	}

	var alternateIPs []net.IP
	var alternateDNS []string
	for _, addr := range addrs {
		var ip net.IP

		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			continue
		}

		alternateIPs = append(alternateIPs, ip)
		alternateDNS = append(alternateDNS, ip.String())
	}

	// Generate a self signed certificate key (CA is self)
	certPem, keyPem, err := k8scert.GenerateSelfSignedCertKey(hostName, alternateIPs, alternateDNS)
	if err != nil {
		return fail(fmt.Errorf("certificate key could not be created: %w", err))
	}

	// Load the tls certificate
	tlsCert, err := tls.X509KeyPair(certPem, keyPem)
	if err != nil {
		return fail(fmt.Errorf("certificate could not be loaded: %w", err))
	}

	return tlsCert, nil
}
