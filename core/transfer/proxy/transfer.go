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

package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	transferapi "github.com/containerd/containerd/api/services/transfer/v1"
	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/containerd/v2/core/transfer"
	tstreaming "github.com/containerd/containerd/v2/core/transfer/streaming"
	"github.com/containerd/containerd/v2/pkg/oci"
)

// progressDrainTimeout is the maximum time Transfer will wait for the progress
// stream consumer goroutine to finish dispatching any remaining progress events
// after the underlying Transfer RPC has returned.
//
// The server closes the progress stream (deferred stream.Close) before the RPC
// returns, so under normal operation the client Recv loop hits io.EOF promptly
// and this wait is a no-op. The timeout only guards against pathological cases
// (server failing to close the stream, or a user Progress callback blocking
// indefinitely) so that Transfer never hangs forever.
const progressDrainTimeout = 30 * time.Second

type proxyTransferrer struct {
	client        transferapi.TTRPCTransferService
	streamCreator streaming.StreamCreator
}

// NewTransferrer returns a new transferrer which can communicate over a GRPC
// or TTRPC connection using the containerd transfer API
func NewTransferrer(client any, sc streaming.StreamCreator) transfer.Transferrer {
	switch c := client.(type) {
	case transferapi.TransferClient:
		return &proxyTransferrer{
			client:        convertClient{c},
			streamCreator: sc,
		}
	case grpc.ClientConnInterface:
		return &proxyTransferrer{
			client:        convertClient{transferapi.NewTransferClient(c)},
			streamCreator: sc,
		}
	case transferapi.TTRPCTransferService:
		return &proxyTransferrer{
			client:        c,
			streamCreator: sc,
		}
	case *ttrpc.Client:
		return &proxyTransferrer{
			client:        transferapi.NewTTRPCTransferClient(c),
			streamCreator: sc,
		}
	case transfer.Transferrer:
		return c
	default:
		panic(fmt.Errorf("unsupported stream client %T: %w", client, errdefs.ErrNotImplemented))
	}
}

type convertClient struct {
	transferapi.TransferClient
}

func (c convertClient) Transfer(ctx context.Context, r *transferapi.TransferRequest) (*emptypb.Empty, error) {
	return c.TransferClient.Transfer(ctx, r)
}

func (p *proxyTransferrer) Transfer(ctx context.Context, src any, dst any, opts ...transfer.Opt) error {
	o := &transfer.Config{}
	for _, opt := range opts {
		opt(o)
	}
	apiOpts := &transferapi.TransferOptions{}

	// When a progress callback is configured, a progress stream is created and a
	// goroutine consumes it, dispatching each event to the caller's callback.
	//
	// The server closes the progress stream as the Transfer RPC returns, but the
	// events it already sent may still be buffered on the client side and not yet
	// Recv'd/dispatched when Transfer returns. Without waiting for the consumer
	// goroutine to drain, callers that inspect progress immediately after Transfer
	// returns can observe a truncated set of events (e.g. missing the final
	// "saved"/"Completed" events). This was the root cause of the flaky
	// TestTransferImport/DigestRefs (see issue #10786).
	//
	// done is closed when the consumer goroutine exits (on io.EOF once the server
	// closes the stream, or on any recv error). After the RPC returns we wait for
	// done (bounded by progressDrainTimeout) so that all already-sent progress
	// events are delivered before Transfer returns.
	var done chan struct{}
	if o.Progress != nil {
		sid := tstreaming.GenerateID("progress")
		stream, err := p.streamCreator.Create(ctx, sid)
		if err != nil {
			return err
		}
		apiOpts.ProgressStream = sid
		done = make(chan struct{})
		go func() {
			defer close(done)
			for {
				a, err := stream.Recv()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						log.G(ctx).WithError(err).Error("progress stream failed to recv")
					}
					return
				}
				i, err := typeurl.UnmarshalAny(a)
				if err != nil {
					log.G(ctx).WithError(err).Warnf("failed to unmarshal progress object: %v", a.GetTypeUrl())
				}
				switch v := i.(type) {
				case *transfertypes.Progress:
					var descp *ocispec.Descriptor
					if v.Desc != nil {
						desc := oci.DescriptorFromProto(v.Desc)
						descp = &desc
					}
					o.Progress(transfer.Progress{
						Event:    v.Event,
						Name:     v.Name,
						Parents:  v.Parents,
						Progress: v.Progress,
						Total:    v.Total,
						Desc:     descp,
					})
				default:
					log.G(ctx).Warnf("unhandled progress object %T: %v", i, a.GetTypeUrl())
				}
			}
		}()
	}
	asrc, err := p.marshalAny(ctx, src)
	if err != nil {
		return err
	}
	adst, err := p.marshalAny(ctx, dst)
	if err != nil {
		return err
	}
	req := &transferapi.TransferRequest{
		Source: &anypb.Any{
			TypeUrl: asrc.GetTypeUrl(),
			Value:   asrc.GetValue(),
		},
		Destination: &anypb.Any{
			TypeUrl: adst.GetTypeUrl(),
			Value:   adst.GetValue(),
		},
		Options: apiOpts,
	}
	_, err = p.client.Transfer(ctx, req)
	if done != nil {
		// Wait for the progress consumer to finish dispatching any events the
		// server sent before closing the stream. This runs on both success and
		// error paths so that progress delivery semantics are consistent. The
		// timeout guards against a server that fails to close the stream or a
		// user callback that blocks indefinitely.
		select {
		case <-done:
		case <-time.After(progressDrainTimeout):
			log.G(ctx).Warn("timed out waiting for transfer progress stream to drain")
		}
	}
	return errgrpc.ToNative(err)
}
func (p *proxyTransferrer) marshalAny(ctx context.Context, i any) (typeurl.Any, error) {
	switch m := i.(type) {
	case streamMarshaler:
		return m.MarshalAny(ctx, p.streamCreator)
	}
	return typeurl.MarshalAny(i)
}

type streamMarshaler interface {
	MarshalAny(context.Context, streaming.StreamCreator) (typeurl.Any, error)
}
