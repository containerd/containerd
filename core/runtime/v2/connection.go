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

package v2

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/containerd/containerd/v2/pkg/dialer"
	shimclient "github.com/containerd/containerd/v2/pkg/shim"
)

// makeConnection creates a new TTRPC or GRPC connection object from address.
// address can be either a socket path for TTRPC or JSON serialized BootstrapParams.
func makeConnection(ctx context.Context, id string, params shimclient.BootstrapParams, onClose func()) (_ io.Closer, retErr error) {
	log.G(ctx).WithFields(log.Fields{
		"address":  params.Address,
		"protocol": params.Protocol,
		"version":  params.Version,
	}).Infof("connecting to shim %s", id)

	switch strings.ToLower(params.Protocol) {
	case "ttrpc":
		conn, err := shimclient.Connect(params.Address, shimclient.AnonReconnectDialer)
		if err != nil {
			return nil, fmt.Errorf("failed to create TTRPC connection: %w", err)
		}
		defer func() {
			if retErr != nil {
				conn.Close()
			}
		}()

		return ttrpc.NewClient(conn, ttrpc.WithOnClose(onClose)), nil
	case "grpc":
		gopts := []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}
		return grpcDialContext(params.Address, onClose, gopts...)
	default:
		return nil, fmt.Errorf("unexpected protocol: %q", params.Protocol)
	}
}

// grpcDialContext and the underlying grpcConn type exist solely
// so we can have something similar to ttrpc.WithOnClose to have
// a callback run when the connection is severed or explicitly closed.
func grpcDialContext(
	address string,
	onClose func(),
	gopts ...grpc.DialOption,
) (*grpcConn, error) {
	// If grpc.WithBlock is specified in gopts this causes the connection to block waiting for
	// a connection regardless of if the socket exists or has a listener when Dial begins. This
	// specific behavior of WithBlock is mostly undesirable for shims, as if the socket isn't
	// there when we go to load/connect there's likely an issue. However, getting rid of WithBlock is
	// also undesirable as we don't want the background connection behavior, we want to ensure
	// a connection before moving on. To bring this in line with the ttrpc connection behavior
	// lets do an initial dial to ensure the shims socket is actually available. stat wouldn't suffice
	// here as if the shim exited unexpectedly its socket may still be on the filesystem, but it'd return
	// ECONNREFUSED which grpc.DialContext will happily trudge along through for the full timeout.
	//
	// This is especially helpful on restart of containerd as if the shim died while containerd
	// was down, we end up waiting the full timeout.
	conn, err := net.DialTimeout("unix", address, time.Second*10)
	if err != nil {
		return nil, err
	}
	conn.Close()

	target := dialer.DialAddress(address)
	client, err := grpc.NewClient(target, gopts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GRPC connection: %w", err)
	}

	done := make(chan struct{})
	go func() {
		gctx := context.Background()
		sourceState := connectivity.Ready
		for {
			if client.WaitForStateChange(gctx, sourceState) {
				state := client.GetState()
				if state == connectivity.Idle || state == connectivity.Shutdown {
					break
				}
				// Could be transient failure. Lets see if we can get back to a working
				// state.
				log.G(gctx).WithFields(log.Fields{
					"state": state,
					"addr":  target,
				}).Warn("shim grpc connection unexpected state")
				sourceState = state
			}
		}
		onClose()
		close(done)
	}()

	return &grpcConn{
		ClientConn:  client,
		onCloseDone: done,
	}, nil
}

type grpcConn struct {
	*grpc.ClientConn
	onCloseDone chan struct{}
}

func (gc *grpcConn) UserOnCloseWait(ctx context.Context) error {
	select {
	case <-gc.onCloseDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
