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
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	contentapi "github.com/containerd/containerd/api/services/content/v1"
	"github.com/containerd/containerd/v2/core/content"
	digest "github.com/opencontainers/go-digest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type mockContentServer struct {
	contentapi.UnimplementedContentServer
}

func (mockContentServer) Write(stream contentapi.Content_WriteServer) error {
	var data []byte
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if req.Ref == "fail-stat" {
			return status.Error(codes.Internal, "negotiation failed")
		}
		data = append(data, req.Data...)
		resp := &contentapi.WriteContentResponse{
			Action: req.Action,
			Offset: int64(len(data)),
			Digest: digest.FromBytes(data).String(),
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
		if req.Action == contentapi.WriteAction_COMMIT {
			return nil
		}
	}
}

func countStreamGoroutines() int {
	b := make([]byte, 4<<20)
	n := runtime.Stack(b, true)
	count := 0
	for _, stack := range strings.Split(string(b[:n]), "\n\n") {
		if strings.Contains(stack, "grpc.newClientStreamWithParams.func") {
			count++
		}
	}
	return count
}

func TestWriterReleasesStreamGoroutines(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	contentapi.RegisterContentServer(server, mockContentServer{})
	go server.Serve(listener)
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///buf",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return net.Dial("tcp", listener.Addr().String())
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	store := NewContentStore(contentapi.NewContentClient(conn))

	for _, mode := range []string{"commit", "close", "fail-stat"} {
		t.Run(mode, func(t *testing.T) {
			before := countStreamGoroutines()
			for i := 0; i < 10; i++ {
				w, err := store.Writer(context.Background(), content.WithRef(mode))
				if mode == "fail-stat" {
					if err == nil {
						t.Fatal("expected negotiation failure")
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if mode == "commit" {
					if _, err = w.Write([]byte("payload")); err != nil {
						t.Fatal(err)
					}
					if err = w.Commit(context.Background(), 7, digest.FromString("payload")); err != nil {
						t.Fatal(err)
					}
				} else {
					if err = w.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			deadline := time.Now().Add(2 * time.Second)
			for countStreamGoroutines() > before && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if after := countStreamGoroutines(); after > before {
				t.Fatalf("stream goroutines leaked: before=%d after=%d", before, after)
			}
		})
	}
}
