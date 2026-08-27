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

package shim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	api "github.com/containerd/containerd/api/services/streaming/v1"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"

	"github.com/containerd/containerd/v2/core/streaming"
	transferstreaming "github.com/containerd/containerd/v2/core/transfer/streaming"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
)

// emptyResponse is the ack sent once a stream is registered.
var emptyResponse typeurl.Any

func init() {
	var err error
	emptyResponse, err = typeurl.MarshalAny(&ptypes.Empty{})
	if err != nil {
		panic(err)
	}
}

// StreamManager serves the containerd streaming API on a shim's ttrpc server and hands
// the streams containerd opens to shim code. containerd addresses a stream by ID against
// the shim's endpoint, for example `ttrpc+vsock://2:10789?streaming_id=portforward-abc-in`.
type StreamManager struct {
	mu      sync.Mutex
	entries map[string]*streamEntry
}

type streamEntry struct {
	ready chan struct{}
	s     streaming.Stream
}

// NewStreamManager returns a StreamManager with no streams registered.
func NewStreamManager() *StreamManager {
	return &StreamManager{entries: make(map[string]*streamEntry)}
}

// RegisterTTRPC registers the streaming service on srv, so containerd can open streams
// against this shim.
func (m *StreamManager) RegisterTTRPC(srv *ttrpc.Server) {
	api.RegisterTTRPCStreamingService(srv, &ttrpcStreamingService{manager: m})
}

// entryLocked returns the entry for id, creating it on first reference by either Get or
// Register. m.mu must be held.
func (m *StreamManager) entryLocked(id string) *streamEntry {
	e, ok := m.entries[id]
	if !ok {
		e = &streamEntry{ready: make(chan struct{})}
		m.entries[id] = e
	}
	return e
}

// Register associates s with id and unblocks any pending Get. The first caller for an id
// wins; the rest get errdefs.ErrAlreadyExists.
func (m *StreamManager) Register(id string, s streaming.Stream) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.entryLocked(id)
	select {
	case <-e.ready:
		return fmt.Errorf("stream %q: %w", id, errdefs.ErrAlreadyExists)
	default:
	}
	e.s = s
	close(e.ready)
	return nil
}

// Get returns the stream registered under id, blocking until containerd opens it or ctx
// is done. Waiting reserves id; the reservation is dropped if ctx ends first.
func (m *StreamManager) Get(ctx context.Context, id string) (streaming.Stream, error) {
	m.mu.Lock()
	e := m.entryLocked(id)
	m.mu.Unlock()

	select {
	case <-e.ready:
		return e.s, nil
	case <-ctx.Done():
		m.dropPending(id, e)
		return nil, ctx.Err()
	}
}

// dropPending removes id if e is still its entry and no stream arrived, so a later
// Register starts fresh.
func (m *StreamManager) dropPending(id string, e *streamEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries[id] != e {
		return
	}
	select {
	case <-e.ready:
	default:
		delete(m.entries, id)
	}
}

// Delete drops the stream registered under id. It does not close the stream.
func (m *StreamManager) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, id)
}

// deleteStream drops id only if s is still registered under it.
func (m *StreamManager) deleteStream(id string, s streaming.Stream) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[id]; ok && e.s == s {
		delete(m.entries, id)
	}
}

// OpenPortForward waits for the "<streamID>-in" and "<streamID>-out" streams and presents
// them as a duplex pipe to copy the sandbox connection against.
//
// containerd opens those streams only after the PortForward RPC returns, so call this from
// a separate goroutine. ctx bounds the wait only, not the returned pipe, and should carry
// a deadline: the wait is otherwise indefinite.
func (m *StreamManager) OpenPortForward(ctx context.Context, streamID string) (io.ReadWriteCloser, error) {
	inID, outID := streamID+"-in", streamID+"-out"

	in, err := m.Get(ctx, inID)
	if err != nil {
		return nil, fmt.Errorf("failed to get port forward input stream %q: %w", inID, err)
	}
	out, err := m.Get(ctx, outID)
	if err != nil {
		m.Delete(inID)
		in.Close()
		return nil, fmt.Errorf("failed to get port forward output stream %q: %w", outID, err)
	}

	// Close cancels this, ending the goroutines the byte streams start.
	pipeCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &portForwardStreams{
		r:       transferstreaming.ReadByteStream(pipeCtx, in),
		w:       transferstreaming.WriteByteStream(pipeCtx, out),
		cancel:  cancel,
		manager: m,
		ids:     []string{inID, outID},
	}, nil
}

type portForwardStreams struct {
	r       io.ReadCloser
	w       io.WriteCloser
	cancel  context.CancelFunc
	manager *StreamManager
	ids     []string
}

func (p *portForwardStreams) Read(b []byte) (int, error) { return p.r.Read(b) }

func (p *portForwardStreams) Write(b []byte) (int, error) { return p.w.Write(b) }

func (p *portForwardStreams) Close() error {
	for _, id := range p.ids {
		p.manager.Delete(id)
	}
	err := errors.Join(p.r.Close(), p.w.Close())
	p.cancel()
	return err
}

type ttrpcStreamingService struct {
	manager *StreamManager
}

func (s *ttrpcStreamingService) Stream(ctx context.Context, srv api.TTRPCStreaming_StreamServer) error {
	a, err := srv.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return err
		}
		return errgrpc.ToGRPC(err)
	}
	var i api.StreamInit
	if err := typeurl.UnmarshalTo(a, &i); err != nil {
		return errgrpc.ToGRPC(err)
	}

	ss := &serverStream{s: srv, cc: make(chan struct{})}
	if err := s.manager.Register(i.ID, ss); err != nil {
		return errgrpc.ToGRPC(err)
	}
	// Drop the registration when the stream ends, so abandoned streams do not accumulate.
	defer s.manager.deleteStream(i.ID, ss)

	// Ack so the client knows the stream is registered and ready.
	if err := srv.Send(typeurl.MarshalProto(emptyResponse)); err != nil {
		if errors.Is(err, io.EOF) {
			return err
		}
		return errgrpc.ToGRPC(err)
	}

	select {
	case <-ctx.Done():
	case <-ss.cc:
	}
	return nil
}

type serverStream struct {
	s  api.TTRPCStreaming_StreamServer
	cc chan struct{}

	closeOnce sync.Once
}

func (ss *serverStream) Send(a typeurl.Any) error {
	err := ss.s.Send(typeurl.MarshalProto(a))
	if err != nil && !errors.Is(err, io.EOF) {
		err = errgrpc.ToNative(err)
	}
	return err
}

func (ss *serverStream) Recv() (typeurl.Any, error) {
	a, err := ss.s.Recv()
	if err != nil && !errors.Is(err, io.EOF) {
		err = errgrpc.ToNative(err)
	}
	return a, err
}

func (ss *serverStream) Close() error {
	ss.closeOnce.Do(func() { close(ss.cc) })
	return nil
}
