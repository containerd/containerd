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
	"testing"
	"time"

	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestWithTimeout(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		p := &proxySnapshotter{}
		ctx, cancel := p.withTimeout(context.Background())
		defer cancel()
		if _, ok := ctx.Deadline(); ok {
			t.Fatal("expected no deadline when defaultTimeout is unset")
		}
	})

	t.Run("applied", func(t *testing.T) {
		p := &proxySnapshotter{defaultTimeout: time.Minute}
		ctx, cancel := p.withTimeout(context.Background())
		defer cancel()
		dl, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected a deadline")
		}
		if d := time.Until(dl); d <= 0 || d > time.Minute {
			t.Fatalf("deadline %v is not within the default timeout", d)
		}
	})

	t.Run("caller deadline kept", func(t *testing.T) {
		p := &proxySnapshotter{defaultTimeout: time.Minute}
		want := time.Now().Add(2 * time.Hour)
		parent, cancelParent := context.WithDeadline(context.Background(), want)
		defer cancelParent()

		ctx, cancel := p.withTimeout(parent)
		defer cancel()
		got, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected a deadline")
		}
		if !got.Equal(want) {
			t.Fatalf("deadline %v was shortened, want %v", got, want)
		}
	})
}

// blockedClient blocks on Remove until its context is done. Other methods are
// unimplemented and panic if called.
type blockedClient struct {
	snapshotsapi.SnapshotsClient
}

func (blockedClient) Remove(ctx context.Context, _ *snapshotsapi.RemoveSnapshotRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRemoveAppliesDefaultTimeout(t *testing.T) {
	s := NewSnapshotter(blockedClient{}, "test", WithDefaultTimeout(50*time.Millisecond))

	done := make(chan error, 1)
	go func() { done <- s.Remove(context.Background(), "key") }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Remove returned no error against a blocked client")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Remove did not return, so the default timeout was not applied")
	}
}
