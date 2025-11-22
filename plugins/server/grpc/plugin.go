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

package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/stats"

	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/internal/wintls"
	"github.com/containerd/containerd/v2/pkg/sys"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/server/internal"
)

type config struct {
	Address        string `toml:"address"`
	UID            int    `toml:"uid"`
	GID            int    `toml:"gid"`
	MaxRecvMsgSize int    `toml:"max_recv_message_size"`
	MaxSendMsgSize int    `toml:"max_send_message_size"`
}

type tcpConfig struct {
	Address        string `toml:"address"`
	TLSCA          string `toml:"tls_ca"`
	TLSCert        string `toml:"tls_cert"`
	TLSKey         string `toml:"tls_key"`
	TLSCName       string `toml:"tls_common_name"`
	MaxRecvMsgSize int    `toml:"max_recv_message_size"`
	MaxSendMsgSize int    `toml:"max_send_message_size"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.ServerPlugin,
		ID:   "grpc",
		Requires: []plugin.Type{
			plugins.GRPCPlugin,
			plugins.MetricsPlugin,
		},
		Config: &config{
			Address:        defaults.DefaultAddress,
			MaxRecvMsgSize: defaults.DefaultMaxRecvMsgSize,
			MaxSendMsgSize: defaults.DefaultMaxSendMsgSize,
		},
		InitFn: func(ic *plugin.InitContext) (any, error) {
			c := ic.Config.(*config)
			if c.Address == "" {
				return nil, fmt.Errorf("grpc address cannot be empty: %w", errdefs.ErrInvalidArgument)
			}

			var (
				streamOpts              = []grpc.StreamServerInterceptor{streamNamespaceInterceptor}
				unaryOpts               = []grpc.UnaryServerInterceptor{unaryNamespaceInterceptor}
				serverOpts              = []grpc.ServerOption{}
				prometheusServerMetrics *grpc_prometheus.ServerMetrics // This should be grpc handler
			)

			if p, err := ic.GetByID(plugins.MetricsPlugin, "grpc-prometheus"); err == nil {
				prometheusServerMetrics = p.(*grpc_prometheus.ServerMetrics)
				streamOpts = append(streamOpts, prometheusServerMetrics.StreamServerInterceptor())
				unaryOpts = append(unaryOpts, prometheusServerMetrics.UnaryServerInterceptor())
			}

			if p, err := ic.GetByID(plugins.MetricsPlugin, "grpc-otel"); err == nil {
				serverOpts = append(serverOpts, grpc.StatsHandler(p.(stats.Handler)))
			}

			serverOpts = append(serverOpts, grpc.ChainStreamInterceptor(streamOpts...))
			serverOpts = append(serverOpts, grpc.ChainUnaryInterceptor(unaryOpts...))
			if c.MaxRecvMsgSize > 0 {
				serverOpts = append(serverOpts, grpc.MaxRecvMsgSize(c.MaxRecvMsgSize))
			}
			if c.MaxSendMsgSize > 0 {
				serverOpts = append(serverOpts, grpc.MaxSendMsgSize(c.MaxSendMsgSize))
			}

			// grpcService allows GRPC services to be registered with the underlying server
			type grpcService interface {
				Register(*grpc.Server) error
			}

			s := grpc.NewServer(serverOpts...)
			ps, err := ic.GetByType(plugins.GRPCPlugin) // ensure grpc plugin is initialized
			if err != nil {
				return nil, err
			}
			for _, p := range ps {
				if gs, isGRPC := p.(grpcService); isGRPC {
					if err := gs.Register(s); err != nil {
						return nil, fmt.Errorf("failed to register grpc service: %w", err)
					}
				}
			}
			if prometheusServerMetrics != nil {
				prometheusServerMetrics.InitializeMetrics(s)
			}
			return grpcServer{
				Server: s,
				config: *c,
			}, nil
		},
	})

	registry.Register(&plugin.Registration{
		Type: plugins.ServerPlugin,
		ID:   "grpc-tcp",
		Requires: []plugin.Type{
			plugins.GRPCPlugin,
			plugins.MetricsPlugin,
		},
		Config: &tcpConfig{
			MaxRecvMsgSize: defaults.DefaultMaxRecvMsgSize,
			MaxSendMsgSize: defaults.DefaultMaxSendMsgSize,
		},
		InitFn: func(ic *plugin.InitContext) (any, error) {
			c := ic.Config.(*tcpConfig)
			if c.Address == "" {
				return nil, plugin.ErrSkipPlugin
			}

			var (
				streamOpts              = []grpc.StreamServerInterceptor{streamNamespaceInterceptor}
				unaryOpts               = []grpc.UnaryServerInterceptor{unaryNamespaceInterceptor}
				serverOpts              = []grpc.ServerOption{}
				prometheusServerMetrics *grpc_prometheus.ServerMetrics // This should be grpc handler
			)

			if p, err := ic.GetByID(plugins.MetricsPlugin, "grpc-prometheus"); err == nil {
				prometheusServerMetrics = p.(*grpc_prometheus.ServerMetrics)
				streamOpts = append(streamOpts, prometheusServerMetrics.StreamServerInterceptor())
				unaryOpts = append(unaryOpts, prometheusServerMetrics.UnaryServerInterceptor())
				prometheusServerMetrics.InitializeMetrics(nil)
			}

			if p, err := ic.GetByID(plugins.MetricsPlugin, "grpc-otel"); err == nil {
				serverOpts = append(serverOpts, grpc.StatsHandler(p.(stats.Handler)))
			}

			serverOpts = append(serverOpts, grpc.ChainStreamInterceptor(streamOpts...))
			serverOpts = append(serverOpts, grpc.ChainUnaryInterceptor(unaryOpts...))

			if c.MaxRecvMsgSize > 0 {
				serverOpts = append(serverOpts, grpc.MaxRecvMsgSize(c.MaxRecvMsgSize))
			}
			if c.MaxSendMsgSize > 0 {
				serverOpts = append(serverOpts, grpc.MaxSendMsgSize(c.MaxSendMsgSize))
			}

			if c.TLSCert != "" {
				log.G(ic.Context).Info("setting up tls on tcp GRPC services...")

				tlsCert, err := tls.LoadX509KeyPair(c.TLSCert, c.TLSKey)
				if err != nil {
					return nil, err
				}
				tlsConfig := &tls.Config{Certificates: []tls.Certificate{tlsCert}}

				if c.TLSCA != "" {
					caCertPool := x509.NewCertPool()
					caCert, err := os.ReadFile(c.TLSCA)
					if err != nil {
						return nil, fmt.Errorf("failed to load CA file: %w", err)
					}
					caCertPool.AppendCertsFromPEM(caCert)
					tlsConfig.ClientCAs = caCertPool
					tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
				}
				serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(tlsConfig)))
			} else if c.TLSCName != "" {
				tlsConfig, CA, res, err :=
					wintls.SetupTLSFromWindowsCertStore(ic.Context, c.TLSCName)
				if err != nil {
					return nil, fmt.Errorf("failed to setup TLS from Windows cert store: %w", err)
				}
				// Cache resource for cleanup (Windows only)
				setTLSResource(res)
				if CA != nil {
					tlsConfig.ClientCAs = CA
					tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
				}
				serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(tlsConfig)))
			}

			// tcpService allows GRPC services to be registered with the underlying tcp server
			type tcpService interface {
				RegisterTCP(*grpc.Server) error
			}

			s := grpc.NewServer(serverOpts...)
			ps, err := ic.GetByType(plugins.GRPCPlugin) // ensure grpc plugin is initialized
			if err != nil {
				return nil, err
			}
			var hasService bool
			for _, p := range ps {
				if gs, isGRPC := p.(tcpService); isGRPC {
					if err := gs.RegisterTCP(s); err != nil {
						return nil, fmt.Errorf("failed to register grpc service: %w", err)
					}
					hasService = true
				}
			}
			if !hasService {
				return nil, fmt.Errorf("no tcp grpc services configured: %w", plugin.ErrSkipPlugin)
			}
			if prometheusServerMetrics != nil {
				prometheusServerMetrics.InitializeMetrics(s)
			}
			return tcpServer{
				Server: s,
				config: *c,
			}, nil
		},
	})
}

type grpcServer struct {
	*grpc.Server
	config config
}

func (s grpcServer) Start(ctx context.Context) error {
	log.G(ctx).Infof("starting GRPC server on %s with %d/%d", s.config.Address, s.config.UID, s.config.GID)
	l, err := sys.GetLocalListener(s.config.Address, s.config.UID, s.config.GID)
	if err != nil {
		return fmt.Errorf("failed to get listener for main endpoint: %w", err)
	}

	internal.Serve(ctx, l, s.Serve)

	return nil
}
func (s grpcServer) Close() error {
	s.Stop()

	return nil
}

type tcpServer struct {
	*grpc.Server
	config tcpConfig
}

func (s tcpServer) Start(ctx context.Context) error {
	l, err := net.Listen("tcp", s.config.Address)
	if err != nil {
		return fmt.Errorf("failed to get listener for TCP grpc endpoint: %w", err)
	}

	internal.Serve(ctx, l, s.Serve)

	return nil
}

func (s tcpServer) Close() error {
	s.Stop()

	// Clean up TLS resources (Windows only)
	cleanupTLSResources()

	return nil
}
