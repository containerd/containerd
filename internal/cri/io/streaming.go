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

package io

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/containerd/ttrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	streamingapi "github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/containerd/v2/core/streaming/proxy"
	"github.com/containerd/containerd/v2/core/transfer/streaming"
	"github.com/containerd/containerd/v2/pkg/shim"
)

// ioStream is a stream created by streaming api for io transfer
// we add a field c as io.Closer because we do connect the streaming server
// and create a client everytime we create a stream. so we need to close
// the connection if the stream is closed.
type ioStream struct {
	streamingapi.Stream
	c io.Closer
}

func (i *ioStream) Close() error {
	return errors.Join(i.Stream.Close(), i.c.Close())
}

func openStdinStream(ctx context.Context, url string) (io.WriteCloser, error) {
	stream, err := openStream(ctx, url)
	if err != nil {
		return nil, err
	}
	return streaming.WriteByteStream(ctx, stream), nil
}

func openOutputStream(ctx context.Context, url string) (io.ReadCloser, error) {
	stream, err := openStream(ctx, url)
	if err != nil {
		return nil, err
	}
	return streaming.ReadByteStream(ctx, stream), nil
}

func openStream(ctx context.Context, urlStr string) (streamingapi.Stream, error) {
	// urlStr should be in the form of:
	// <ttrpc|grpc>+<unix|vsock|hvsock>://<uds-path|vsock-cid:vsock-port|uds-path:hvsock-port>?streaming_id=<stream-id>
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("address url parse error: %v", err)
	}
	// The address returned from sandbox controller should be in the form like ttrpc+unix://<uds-path>
	// or grpc+vsock://<cid>:<port>, we should get the protocol from the url first.
	protocol, scheme, ok := strings.Cut(u.Scheme, "+")
	if !ok {
		return nil, fmt.Errorf("the scheme of sandbox address should be in " +
			" the form of <protocol>+<unix|vsock|tcp>, i.e. ttrpc+unix or grpc+vsock")
	}

	id := u.Query().Get("streaming_id")
	if id == "" {
		return nil, fmt.Errorf("no stream id in url queries")
	}
	realAddress := fmt.Sprintf("%s://%s/%s", scheme, u.Host, u.Path)
	conn, err := shim.AnonReconnectDialer(realAddress, 100*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect the stream %v", err)
	}
	var stream streamingapi.Stream

	switch protocol {
	case "ttrpc":
		c := ttrpc.NewClient(conn)
		streamCreator := proxy.NewStreamCreator(c)
		stream, err = streamCreator.Create(ctx, id)
		if err != nil {
			return nil, err
		}
		return &ioStream{Stream: stream, c: c}, nil

	case "grpc":
		gopts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
		conn, err := grpc.NewClient(realAddress, gopts...)
		if err != nil {
			return nil, err
		}
		streamCreator := proxy.NewStreamCreator(conn)
		stream, err = streamCreator.Create(ctx, id)
		if err != nil {
			return nil, err
		}
		return &ioStream{Stream: stream, c: conn}, nil
	default:
		return nil, fmt.Errorf("protocol not supported")
	}
}
