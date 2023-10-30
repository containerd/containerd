//go:build gofuzz

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

package fuzz

import (
	"context"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/services/server"
	"github.com/containerd/containerd/v2/services/server/config"
	"github.com/containerd/containerd/v2/sys"
	"github.com/containerd/log"
)

const (
	defaultRoot    = "/var/lib/containerd"
	defaultState   = "/tmp/containerd"
	defaultAddress = "/tmp/containerd/containerd.sock"
)

var (
	initDaemon sync.Once
)

func startDaemon() {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	errC := make(chan error, 1)

	go func() {
		defer close(errC)

		srvconfig := &config.Config{
			Version: config.CurrentConfigVersion,
			Root:    defaultRoot,
			State:   defaultState,
			Debug: config.Debug{
				Level: "debug",
			},
			GRPC: config.GRPCConfig{
				Address:        defaultAddress,
				MaxRecvMsgSize: defaults.DefaultMaxRecvMsgSize,
				MaxSendMsgSize: defaults.DefaultMaxSendMsgSize,
			},
			DisabledPlugins: []string{},
			RequiredPlugins: []string{},
		}

		server, err := server.New(ctx, srvconfig)
		if err != nil {
			errC <- err
			return
		}

		l, err := sys.GetLocalListener(srvconfig.GRPC.Address, srvconfig.GRPC.UID, srvconfig.GRPC.GID)
		if err != nil {
			errC <- err
			return
		}

		go func() {
			defer l.Close()
			if err := server.ServeGRPC(l); err != nil {
				log.G(ctx).WithError(err).WithField("address", srvconfig.GRPC.Address).Fatal("serve failure")
			}
		}()

		server.Wait()
	}()

	var err error
	select {
	case err = <-errC:
	case <-ctx.Done():
		err = ctx.Err()
	}

	if err != nil {
		panic(err)
	}
}
