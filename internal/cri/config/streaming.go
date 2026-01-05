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

package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	k8snet "k8s.io/apimachinery/pkg/util/net"
	k8scert "k8s.io/client-go/util/cert"

	streaming "github.com/containerd/containerd/v2/internal/cri/streamingserver"
)

type streamListenerMode int

const (
	x509KeyPairTLS streamListenerMode = iota
	selfSignTLS
	withoutTLS
)

func getStreamListenerMode(config *ServerConfig) (streamListenerMode, error) {
	if config.EnableTLSStreaming {
		if config.X509KeyPairStreaming.TLSCertFile != "" && config.X509KeyPairStreaming.TLSKeyFile != "" {
			return x509KeyPairTLS, nil
		}
		if config.X509KeyPairStreaming.TLSCertFile != "" && config.X509KeyPairStreaming.TLSKeyFile == "" {
			return -1, errors.New("must set X509KeyPairStreaming.TLSKeyFile")
		}
		if config.X509KeyPairStreaming.TLSCertFile == "" && config.X509KeyPairStreaming.TLSKeyFile != "" {
			return -1, errors.New("must set X509KeyPairStreaming.TLSCertFile")
		}
		return selfSignTLS, nil
	}
	if config.X509KeyPairStreaming.TLSCertFile != "" {
		return -1, errors.New("X509KeyPairStreaming.TLSCertFile is set but EnableTLSStreaming is not set")
	}
	if config.X509KeyPairStreaming.TLSKeyFile != "" {
		return -1, errors.New("X509KeyPairStreaming.TLSKeyFile is set but EnableTLSStreaming is not set")
	}
	return withoutTLS, nil
}

func (c *ServerConfig) StreamingConfig() (streaming.Config, error) {
	var (
		addr              = c.StreamServerAddress
		port              = c.StreamServerPort
		streamIdleTimeout = c.StreamIdleTimeout
	)
	if addr == "" {
		a, err := k8snet.ResolveBindAddress(nil)
		if err != nil {
			return streaming.Config{}, fmt.Errorf("failed to get stream server address: %w", err)
		}
		addr = a.String()
	}
	config := streaming.DefaultConfig
	if streamIdleTimeout != "" {
		var err error
		config.StreamIdleTimeout, err = time.ParseDuration(streamIdleTimeout)
		if err != nil {
			return streaming.Config{}, fmt.Errorf("invalid stream idle timeout: %w", err)
		}
	}
	config.Addr = net.JoinHostPort(addr, port)

	tlsMode, err := getStreamListenerMode(c)
	if err != nil {
		return streaming.Config{}, fmt.Errorf("invalid stream server configuration: %w", err)
	}
	switch tlsMode {
	case x509KeyPairTLS:
		tlsCert, err := tls.LoadX509KeyPair(c.X509KeyPairStreaming.TLSCertFile, c.X509KeyPairStreaming.TLSKeyFile)
		if err != nil {
			return streaming.Config{}, fmt.Errorf("failed to load x509 key pair for stream server: %w", err)
		}
		config.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		}
	case selfSignTLS:
		tlsCert, err := newTLSCert()
		if err != nil {
			return streaming.Config{}, fmt.Errorf("failed to generate tls certificate for stream server: %w", err)
		}
		config.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		}
	case withoutTLS:
	default:
		return streaming.Config{}, errors.New("invalid configuration for the stream listener")
	}
	return config, nil
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
