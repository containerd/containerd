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

package registry

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/typeurl/v2"
)

// pipeStream is an in-memory streaming.Stream used to connect the client-side
// credential responder (spawned by OCIRegistry.MarshalAny) to the server-side
// credCallback within a single process.
type pipeStream struct {
	sendCh chan typeurl.Any
	recvCh chan typeurl.Any
	closed chan struct{}
	once   *sync.Once
}

func (s *pipeStream) Send(a typeurl.Any) error {
	select {
	case s.sendCh <- a:
		return nil
	case <-s.closed:
		return io.EOF
	}
}

func (s *pipeStream) Recv() (typeurl.Any, error) {
	select {
	case a := <-s.recvCh:
		return a, nil
	case <-s.closed:
		return nil, io.EOF
	}
}

func (s *pipeStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

// newStreamPipe returns two connected in-memory streams that share one
// shutdown signal, so closing either endpoint unblocks the peer's Recv with
// io.EOF -- the teardown behavior of a real bidirectional stream.
func newStreamPipe() (client, server *pipeStream) {
	c2s := make(chan typeurl.Any)
	s2c := make(chan typeurl.Any)
	closed := make(chan struct{})
	once := &sync.Once{}
	client = &pipeStream{sendCh: c2s, recvCh: s2c, closed: closed, once: once}
	server = &pipeStream{sendCh: s2c, recvCh: c2s, closed: closed, once: once}
	return client, server
}

type singleStreamCreator struct{ s streaming.Stream }

func (c singleStreamCreator) Create(context.Context, string) (streaming.Stream, error) {
	return c.s, nil
}

type failingCredentialHelper struct{}

func (failingCredentialHelper) GetCredentials(context.Context, string, string) (Credentials, error) {
	return Credentials{}, errors.New("credential backend unavailable")
}

type staticCredentialHelper struct{ creds Credentials }

func (h staticCredentialHelper) GetCredentials(context.Context, string, string) (Credentials, error) {
	return h.creds, nil
}

// TestCredentialResponderRepliesOnHelperError verifies that when the client-side
// credential helper returns an error, the responder goroutine started by
// OCIRegistry.MarshalAny still answers the request, so the server-side
// credCallback.GetCredentials does not block forever in stream.Recv().
//
// Before the fix the responder logged the error and `continue`d without sending
// a response, so this test deadlocks and fails via the timeout below.
func TestCredentialResponderRepliesOnHelperError(t *testing.T) {
	ctx := t.Context()
	clientStream, serverStream := newStreamPipe()
	defer clientStream.Close()
	defer serverStream.Close()

	reg := &OCIRegistry{
		reference: "docker.io/library/test",
		creds:     failingCredentialHelper{},
	}
	if _, err := reg.MarshalAny(ctx, singleStreamCreator{s: clientStream}); err != nil {
		t.Fatalf("MarshalAny: %v", err)
	}

	cc := &credCallback{stream: serverStream}
	type result struct {
		creds Credentials
		err   error
	}
	done := make(chan result, 1)
	go func() {
		creds, err := cc.GetCredentials(ctx, "docker.io/library/test", "registry-1.docker.io")
		done <- result{creds, err}
	}()

	select {
	case got := <-done:
		// The responder replied, so there is no deadlock. The reply must also be
		// the anonymous fallback: a helper error resolves to empty credentials
		// (AuthType_NONE), not an error.
		if got.err != nil {
			t.Fatalf("GetCredentials returned an error after a helper failure: %v", got.err)
		}
		if got.creds.Username != "" || got.creds.Secret != "" || got.creds.Header != "" {
			t.Fatalf("expected anonymous (empty) credentials, got %+v", got.creds)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("credCallback.GetCredentials hung: responder did not reply after credential helper error")
	}
}

// TestCredentialResponderRepliesOnUnmarshalError verifies that a request the
// responder cannot unmarshal into an AuthRequest still draws exactly one reply,
// so the peer is not left blocked in Recv(). The reply is the NONE fallback.
// The credential helper returns real credentials, so a NONE reply proves the
// unmarshal-error branch answered rather than the helper being consulted.
func TestCredentialResponderRepliesOnUnmarshalError(t *testing.T) {
	clientStream, serverStream := newStreamPipe()
	defer clientStream.Close()
	defer serverStream.Close()

	reg := &OCIRegistry{
		reference: "docker.io/library/test",
		creds:     staticCredentialHelper{creds: Credentials{Username: "user", Secret: "secret"}},
	}
	if _, err := reg.MarshalAny(t.Context(), singleStreamCreator{s: clientStream}); err != nil {
		t.Fatalf("MarshalAny: %v", err)
	}

	// An AuthResponse is not an AuthRequest, so the responder's UnmarshalTo fails.
	bogus, err := typeurl.MarshalAny(&transfertypes.AuthResponse{})
	if err != nil {
		t.Fatalf("marshal bogus request: %v", err)
	}

	type result struct {
		any typeurl.Any
		err error
	}
	done := make(chan result, 1)
	go func() {
		if err := serverStream.Send(bogus); err != nil {
			done <- result{err: err}
			return
		}
		a, err := serverStream.Recv()
		done <- result{any: a, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("stream exchange failed: %v", got.err)
		}
		var resp transfertypes.AuthResponse
		if err := typeurl.UnmarshalTo(got.any, &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if resp.AuthType != transfertypes.AuthType_NONE {
			t.Fatalf("expected AuthType_NONE for an unmarshalable request, got %v", resp.AuthType)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("responder did not reply to an unmarshalable request")
	}
}

// TestCredentialResponderRemainsUsableAfterError verifies that the stream stays
// usable for later credential requests after one request fails to resolve. This
// is the property that justifies replying NONE rather than closing the stream,
// which is shared across every credential request in a transfer.
func TestCredentialResponderRemainsUsableAfterError(t *testing.T) {
	ctx := t.Context()
	clientStream, serverStream := newStreamPipe()
	defer clientStream.Close()
	defer serverStream.Close()

	reg := &OCIRegistry{
		reference: "docker.io/library/test",
		creds:     failingCredentialHelper{},
	}
	if _, err := reg.MarshalAny(ctx, singleStreamCreator{s: clientStream}); err != nil {
		t.Fatalf("MarshalAny: %v", err)
	}

	cc := &credCallback{stream: serverStream}
	for i := 1; i <= 2; i++ {
		done := make(chan error, 1)
		go func() {
			_, err := cc.GetCredentials(ctx, "docker.io/library/test", "registry-1.docker.io")
			done <- err
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("credCallback.GetCredentials hung on request %d after a prior helper error", i)
		}
	}
}
