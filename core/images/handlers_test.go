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

package images

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func newDesc(id string) ocispec.Descriptor {
	return ocispec.Descriptor{
		Digest:    digest.FromString(id),
		MediaType: ocispec.MediaTypeImageLayer,
		Size:      1,
	}
}

func digestSet(digests []digest.Digest) map[digest.Digest]struct{} {
	set := make(map[digest.Digest]struct{}, len(digests))
	for _, d := range digests {
		set[d] = struct{}{}
	}
	return set
}

func TestDispatchTreeTraversal(t *testing.T) {
	root := newDesc("root")
	child1 := newDesc("child1")
	child2 := newDesc("child2")
	grandchild1 := newDesc("grandchild1")

	graph := map[digest.Digest][]ocispec.Descriptor{
		root.Digest:   {child1, child2},
		child1.Digest: {grandchild1},
	}

	var visited []digest.Digest
	var mu sync.Mutex
	handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		mu.Lock()
		visited = append(visited, desc.Digest)
		mu.Unlock()
		return graph[desc.Digest], nil
	})

	err := Dispatch(context.Background(), handler, nil, root)
	require.NoError(t, err)

	set := digestSet(visited)
	assert.Contains(t, set, root.Digest)
	assert.Contains(t, set, child1.Digest)
	assert.Contains(t, set, child2.Digest)
	assert.Contains(t, set, grandchild1.Digest)
	assert.Len(t, visited, 4)
}

func TestDispatchRepeatedReferences(t *testing.T) {
	root := newDesc("root")
	childA := newDesc("A")
	childB := newDesc("B")
	shared := newDesc("shared")

	graph := map[digest.Digest][]ocispec.Descriptor{
		root.Digest:   {childA, childB},
		childA.Digest: {shared},
		childB.Digest: {shared},
	}

	tests := []struct {
		name            string
		dispatch        func(context.Context, Handler, *semaphore.Weighted, ...ocispec.Descriptor) error
		wantTotalCalls  int32
		wantSharedCalls int32
	}{
		{
			name:            "Dispatch visits every reference",
			dispatch:        Dispatch,
			wantTotalCalls:  5,
			wantSharedCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var totalCalls atomic.Int32
			var sharedCalls atomic.Int32
			handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				totalCalls.Add(1)
				if desc.Digest == shared.Digest {
					sharedCalls.Add(1)
				}
				return graph[desc.Digest], nil
			})

			err := tt.dispatch(context.Background(), handler, nil, root)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotalCalls, totalCalls.Load())
			assert.Equal(t, tt.wantSharedCalls, sharedCalls.Load())
		})
	}
}

func TestDispatchErrSkipDesc(t *testing.T) {
	root := newDesc("root")
	child1 := newDesc("child1")
	child2 := newDesc("child2")
	grandchild := newDesc("grandchild")

	graph := map[digest.Digest][]ocispec.Descriptor{
		root.Digest:   {child1, child2},
		child1.Digest: {grandchild},
	}

	var visited []digest.Digest
	var mu sync.Mutex
	handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		mu.Lock()
		visited = append(visited, desc.Digest)
		mu.Unlock()
		if desc.Digest == child1.Digest {
			return graph[desc.Digest], ErrSkipDesc
		}
		return graph[desc.Digest], nil
	})

	err := Dispatch(context.Background(), handler, nil, root)
	require.NoError(t, err)

	set := digestSet(visited)
	assert.Contains(t, set, root.Digest)
	assert.Contains(t, set, child1.Digest)
	assert.Contains(t, set, child2.Digest)
	assert.NotContains(t, set, grandchild.Digest, "grandchild must not be visited when its parent returns ErrSkipDesc")
}

func TestDispatchConcurrencyLimit(t *testing.T) {
	tests := []struct {
		name           string
		limiter        *semaphore.Weighted
		maxConcurrency int32
		numChildren    int
	}{
		{
			name:           "With explicit limiter",
			limiter:        semaphore.NewWeighted(3),
			maxConcurrency: 3,
			numChildren:    3 * 5,
		},
		{
			name:           "With default limiter",
			limiter:        nil,
			maxConcurrency: defaultMaxConcurrency,
			numChildren:    defaultMaxConcurrency * 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newDesc("root")
			children := make([]ocispec.Descriptor, tt.numChildren)
			for i := range tt.numChildren {
				children[i] = newDesc(fmt.Sprintf("child-%d", i))
			}

			graph := map[digest.Digest][]ocispec.Descriptor{
				root.Digest: children,
			}

			var concurrent, peak atomic.Int32
			var exceeded atomic.Bool

			handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				n := concurrent.Add(1)
				if n > tt.maxConcurrency {
					exceeded.Store(true)
				}
				for {
					old := peak.Load()
					if n <= old || peak.CompareAndSwap(old, n) {
						break
					}
				}
				defer concurrent.Add(-1)

				// Keep handlers running long enough to overlap.
				time.Sleep(10 * time.Millisecond)
				return graph[desc.Digest], nil
			})

			err := Dispatch(context.Background(), handler, tt.limiter, root)
			require.NoError(t, err)

			assert.False(t, exceeded.Load(), "concurrent handlers must not exceed capacity")
			assert.Equal(t, tt.maxConcurrency, peak.Load(),
				"concurrent handlers must reach the capacity the caller asked for")
		})
	}
}

func TestDispatchSharedLimiter(t *testing.T) {
	const (
		weight  = 4
		callers = 4
		perTree = 12
	)

	limiter := semaphore.NewWeighted(weight)

	var concurrent atomic.Int32
	var exceeded atomic.Bool
	visited := make([]atomic.Int32, callers)

	var eg errgroup.Group
	for c := range callers {
		eg.Go(func() error {
			root := newDesc(fmt.Sprintf("root-%d", c))
			children := make([]ocispec.Descriptor, perTree)
			for i := range perTree {
				children[i] = newDesc(fmt.Sprintf("caller-%d-child-%d", c, i))
			}
			graph := map[digest.Digest][]ocispec.Descriptor{root.Digest: children}

			handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				if concurrent.Add(1) > weight {
					exceeded.Store(true)
				}
				defer concurrent.Add(-1)
				visited[c].Add(1)

				time.Sleep(2 * time.Millisecond)
				return graph[desc.Digest], nil
			})

			return Dispatch(context.Background(), handler, limiter, root)
		})
	}

	done := make(chan error, 1)
	go func() { done <- eg.Wait() }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("shared-limiter traversals did not complete; a caller is starved or deadlocked")
	}

	assert.False(t, exceeded.Load(), "combined concurrent handlers must not exceed the shared limiter weight")
	for c := range callers {
		assert.Equal(t, int32(perTree+1), visited[c].Load(),
			"caller %d must visit every descriptor in its own graph", c)
	}
}

func TestDispatchReferenceLimit(t *testing.T) {
	// Each root is below the cap on its own; their combined references exceed it.
	rootA := newDesc("root-a")
	rootB := newDesc("root-b")

	childrenA := make([]ocispec.Descriptor, maxReferences/2)
	childrenB := make([]ocispec.Descriptor, maxReferences/2)
	for i := range childrenA {
		childrenA[i] = newDesc(fmt.Sprintf("a-%d", i))
		childrenB[i] = newDesc(fmt.Sprintf("b-%d", i))
	}

	handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		switch desc.Digest {
		case rootA.Digest:
			return childrenA, nil
		case rootB.Digest:
			return childrenB, nil
		}
		return nil, nil
	})

	require.ErrorIs(t, Dispatch(context.Background(), handler, nil, rootA, rootB), errdefs.ErrResourceExhausted)

	acceptRoot := newDesc("accept-root")
	exact := make([]ocispec.Descriptor, maxReferences-1)
	for i := range exact {
		exact[i] = newDesc(fmt.Sprintf("accept-%d", i))
	}
	acceptHandler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if desc.Digest == acceptRoot.Digest {
			return exact, nil
		}
		return nil, nil
	})
	require.NoError(t, Dispatch(context.Background(), acceptHandler, nil, acceptRoot))
}

func TestDispatchRejectsChildrenBeforeVisiting(t *testing.T) {
	root := newDesc("root")
	children := make([]ocispec.Descriptor, maxReferences)
	for i := range children {
		children[i] = newDesc(fmt.Sprintf("node-%d", i))
	}
	var childVisits atomic.Int32
	handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if desc.Digest == root.Digest {
			return children, nil
		}
		childVisits.Add(1)
		return nil, nil
	})

	require.ErrorIs(t, Dispatch(context.Background(), handler, nil, root), errdefs.ErrResourceExhausted)
	assert.Equal(t, int32(0), childVisits.Load(), "children of an over-limit slice must not be visited")
}

func TestDispatchDuplicateRoots(t *testing.T) {
	root := newDesc("root")

	tests := []struct {
		name      string
		dispatch  func(context.Context, Handler, *semaphore.Weighted, ...ocispec.Descriptor) error
		wantCalls int32
	}{
		{"Dispatch visits each root", Dispatch, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				calls.Add(1)
				return nil, nil
			})
			require.NoError(t, tt.dispatch(context.Background(), handler, nil, root, root))
			assert.Equal(t, tt.wantCalls, calls.Load())
		})
	}
}

func TestDispatchCountsDuplicateReferences(t *testing.T) {
	root := newDesc("root")
	dup := newDesc("dup")
	children := make([]ocispec.Descriptor, maxReferences+1)
	for i := range children {
		children[i] = dup
	}
	fanout := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if desc.Digest == root.Digest {
			return children, nil
		}
		return nil, nil
	})
	require.ErrorIs(t, Dispatch(context.Background(), fanout, nil, root), errdefs.ErrResourceExhausted)

	a, b := newDesc("a"), newDesc("b")
	cycle := map[digest.Digest][]ocispec.Descriptor{a.Digest: {b}, b.Digest: {a}}
	cyclic := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		return cycle[desc.Digest], nil
	})
	require.ErrorIs(t, Dispatch(context.Background(), cyclic, nil, a), errdefs.ErrResourceExhausted)
}

func TestDispatchErrorPropagation(t *testing.T) {
	root := newDesc("root")
	child1 := newDesc("child1")
	child2 := newDesc("child2")

	graph := map[digest.Digest][]ocispec.Descriptor{
		root.Digest: {child1, child2},
	}

	errTest := errors.New("handler error")

	handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if desc.Digest == child1.Digest {
			return nil, errTest
		}
		return graph[desc.Digest], nil
	})

	err := Dispatch(context.Background(), handler, nil, root)
	require.ErrorIs(t, err, errTest)
}

func TestDispatchContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		return nil, nil
	})

	err := Dispatch(ctx, handler, nil, newDesc("root"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestDispatchContextCancellationMidFlight(t *testing.T) {
	// Use more descriptors than the limiter admits so cancellation also covers
	// handlers waiting to start.
	const n = defaultMaxConcurrency * 4
	descs := make([]ocispec.Descriptor, n)
	for i := range n {
		descs[i] = newDesc(fmt.Sprintf("cancel-%d", i))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{}, n)
	handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})

	go func() {
		for range defaultMaxConcurrency {
			<-started
		}
		cancel()
	}()

	err := Dispatch(ctx, handler, nil, descs...)
	require.ErrorIs(t, err, context.Canceled)
}

func TestDispatchCancellationWithNoHandlerError(t *testing.T) {
	const n = defaultMaxConcurrency * 4
	descs := make([]ocispec.Descriptor, n)
	for i := range n {
		descs[i] = newDesc(fmt.Sprintf("cancel-queued-%d", i))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if desc.Digest == descs[0].Digest {
			cancel()
		}
		return nil, nil
	})

	err := Dispatch(ctx, handler, nil, descs...)
	require.ErrorIs(t, err, context.Canceled)
}
