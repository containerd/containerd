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
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	contentapi "github.com/containerd/containerd/api/services/content/v1"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/ttrpc"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/content/testsuite"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/containerd/v2/plugins/services/content/contentserver"
)

const readMethod = "/containerd.services.content.v1.Content/Read"

// testServer serves a local content store over gRPC on a unix socket.
type testServer struct {
	backend content.Store
	root    string
	address string
	// streams counts the streams opened per method.
	mu      sync.Mutex
	streams map[string]int
}

func newTestServer(t testing.TB, root string, ls local.LabelStore) *testServer {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test server uses a unix socket")
	}

	backend, err := local.NewLabeledStore(root, ls)
	if err != nil {
		t.Fatal(err)
	}

	// Keep the socket path short, test names can make t.TempDir long.
	sockDir, err := os.MkdirTemp("", "csproxy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })

	s := &testServer{
		backend: backend,
		root:    root,
		address: filepath.Join(sockDir, "content.sock"),
		streams: map[string]int{},
	}

	l, err := net.Listen("unix", s.address)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(
		grpc.MaxRecvMsgSize(defaults.DefaultMaxRecvMsgSize),
		grpc.MaxSendMsgSize(defaults.DefaultMaxSendMsgSize),
		grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			s.mu.Lock()
			s.streams[info.FullMethod]++
			s.mu.Unlock()
			return handler(srv, ss)
		}))
	if err := contentserver.New(backend).(interface{ Register(*grpc.Server) error }).Register(srv); err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(srv.Stop)

	return s
}

func (s *testServer) streamCount(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[method]
}

func (s *testServer) proxy(t testing.TB) content.Store {
	t.Helper()
	conn, err := grpc.NewClient("unix://"+s.address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(defaults.DefaultMaxRecvMsgSize),
			grpc.MaxCallSendMsgSize(defaults.DefaultMaxSendMsgSize),
		))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return NewContentStore(conn)
}

type memoryLabelStore struct {
	l      sync.Mutex
	labels map[digest.Digest]map[string]string
}

func newMemoryLabelStore() *memoryLabelStore {
	return &memoryLabelStore{labels: map[digest.Digest]map[string]string{}}
}

func (mls *memoryLabelStore) Get(d digest.Digest) (map[string]string, error) {
	mls.l.Lock()
	defer mls.l.Unlock()
	return mls.labels[d], nil
}

func (mls *memoryLabelStore) Set(d digest.Digest, labels map[string]string) error {
	mls.l.Lock()
	defer mls.l.Unlock()
	mls.labels[d] = labels
	return nil
}

func (mls *memoryLabelStore) Update(d digest.Digest, update map[string]string) (map[string]string, error) {
	mls.l.Lock()
	defer mls.l.Unlock()
	labels, ok := mls.labels[d]
	if !ok {
		labels = map[string]string{}
	}
	for k, v := range update {
		if v == "" {
			delete(labels, k)
		} else {
			labels[k] = v
		}
	}
	mls.labels[d] = labels
	return labels, nil
}

func TestContentSuite(t *testing.T) {
	testsuite.ContentSuite(t, "proxy", func(ctx context.Context, root string) (context.Context, content.Store, func() error, error) {
		s := newTestServer(t, root, newMemoryLabelStore())
		return ctx, s.proxy(t), func() error { return nil }, nil
	})
}

func randomBlob(t testing.TB, size int) ([]byte, ocispec.Descriptor) {
	t.Helper()
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b, ocispec.Descriptor{Digest: digest.FromBytes(b), Size: int64(size)}
}

func writeBlob(t testing.TB, cs content.Store, b []byte, desc ocispec.Descriptor) {
	t.Helper()
	if err := content.WriteBlob(context.Background(), cs, desc.Digest.String(), bytes.NewReader(b), desc); err != nil {
		t.Fatal(err)
	}
}

func checkRead(t *testing.T, ra content.ReaderAt, expected []byte) {
	t.Helper()
	if ra.Size() != int64(len(expected)) {
		t.Fatalf("unexpected size %d, expected %d", ra.Size(), len(expected))
	}

	// Read sequentially with a small buffer, like decompression does.
	var got bytes.Buffer
	if _, err := io.CopyBuffer(&got, struct{ io.Reader }{content.NewReader(ra)}, make([]byte, 32*1024)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), expected) {
		t.Fatal("sequential read returned unexpected content")
	}

	// Random access still works.
	off := len(expected) / 3
	p := make([]byte, 4096)
	n, err := ra.ReadAt(p, int64(off))
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if !bytes.Equal(p[:n], expected[off:off+n]) {
		t.Fatal("ReadAt returned unexpected content")
	}
}

func TestReaderAtRemote(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, t.TempDir(), nil)
	cs := s.proxy(t)

	b, desc := randomBlob(t, 3*1024*1024+17)
	writeBlob(t, cs, b, desc)

	ra, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		t.Fatal(err)
	}
	defer ra.Close()
	checkRead(t, ra, b)
}

func TestRemoteReaderWindowedStreams(t *testing.T) {
	defer withReadWindow(1024 * 1024)()
	ctx := context.Background()
	s := newTestServer(t, t.TempDir(), nil)
	cs := s.proxy(t)

	b, desc := randomBlob(t, 5*1024*1024+3)
	writeBlob(t, cs, b, desc)

	ra, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		t.Fatal(err)
	}
	defer ra.Close()

	got, err := io.ReadAll(struct{ io.Reader }{content.NewReader(ra)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, b) {
		t.Fatal("unexpected content")
	}
	// The content is read over one stream per window, rather than one stream
	// per ReadAt call.
	if want := (desc.Size + readWindow - 1) / readWindow; int64(s.streamCount(readMethod)) != want {
		t.Fatalf("read streams = %d, want %d", s.streamCount(readMethod), want)
	}
}

func TestRemoteReaderCloseEarly(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, t.TempDir(), nil)
	cs := s.proxy(t)

	b, desc := randomBlob(t, 4*1024*1024)
	writeBlob(t, cs, b, desc)

	ra, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		t.Fatal(err)
	}
	r := content.NewReader(ra)
	p := make([]byte, 1024)
	if _, err := io.ReadFull(r, p); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p, b[:len(p)]) {
		t.Fatal("unexpected content")
	}
	if err := ra.Close(); err != nil {
		t.Fatal(err)
	}
	// The stream is cancelled, so the reader fails rather than hanging or
	// returning more data from a released stream.
	if _, err := io.ReadAll(r); err == nil {
		t.Fatal("expected an error reading after close")
	}
}

// fakeReadClient serves a Read stream from a fixed list of chunks.
type fakeReadClient struct {
	contentapi.TTRPCContentClient
	chunks [][]byte
	err    error
	opened atomic.Int32
}

func (c *fakeReadClient) Read(ctx context.Context, req *contentapi.ReadContentRequest) (contentapi.TTRPCContent_ReadClient, error) {
	c.opened.Add(1)
	return &fakeReadStream{chunks: c.chunks, err: c.err}, nil
}

type fakeReadStream struct {
	ttrpc.ClientStream
	chunks [][]byte
	// err is returned once the chunks are exhausted, io.EOF when nil.
	err error
}

func (s *fakeReadStream) Recv() (*contentapi.ReadContentResponse, error) {
	if len(s.chunks) == 0 {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	c := s.chunks[0]
	s.chunks = s.chunks[1:]
	return &contentapi.ReadContentResponse{Data: c}, nil
}

func TestRemoteReaderShortStream(t *testing.T) {
	client := &fakeReadClient{chunks: [][]byte{[]byte("hello "), []byte("wor")}}
	ra := newRemoteReaderAt(context.Background(), digest.FromString("hello world"), int64(len("hello world")), client)
	defer ra.Close()

	got, err := io.ReadAll(ra.Reader())
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected unexpected EOF, got %v", err)
	}
	if string(got) != "hello wor" {
		t.Fatalf("unexpected content %q", got)
	}
	if n := client.opened.Load(); n != 1 {
		t.Fatalf("expected one stream, got %d", n)
	}
}

func TestRemoteReaderEmpty(t *testing.T) {
	client := &fakeReadClient{}
	ra := newRemoteReaderAt(context.Background(), digest.FromString(""), 0, client)
	got, err := io.ReadAll(ra.Reader())
	if err != nil || len(got) != 0 {
		t.Fatalf("unexpected result %q, %v", got, err)
	}
	if n := client.opened.Load(); n != 0 {
		t.Fatalf("expected no stream for empty content, got %d", n)
	}
}

func TestRemoteReaderReleasesStream(t *testing.T) {
	const data = "hello world"
	for _, tc := range []struct {
		name    string
		chunks  [][]byte
		err     error
		wantErr error
	}{
		{
			name:   "complete",
			chunks: [][]byte{[]byte("hello "), []byte("world")},
		},
		{
			name:    "short",
			chunks:  [][]byte{[]byte("hello ")},
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name:    "error",
			chunks:  [][]byte{[]byte("hello ")},
			err:     errgrpc.ToGRPC(errdefs.ErrUnavailable),
			wantErr: errdefs.ErrUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ra := newRemoteReaderAt(context.Background(), digest.FromString(data), int64(len(data)), &fakeReadClient{chunks: tc.chunks, err: tc.err})
			defer ra.Close()

			r := ra.Reader()
			if _, err := r.Read(make([]byte, 1)); err != nil {
				t.Fatalf("Read() of first byte: %v", err)
			}
			if n := len(ra.streams); n != 1 {
				t.Fatalf("open streams while reading = %d, want 1", n)
			}
			if _, err := io.ReadAll(r); !errors.Is(err, tc.wantErr) {
				t.Fatalf("io.ReadAll() error = %v, want %v", err, tc.wantErr)
			}
			if n := len(ra.streams); n != 0 {
				t.Errorf("open streams after the reader ended = %d, want 0", n)
			}
		})
	}
}

// rangeReadClient serves Read streams from data, honouring the offset and size
// of each request and sending chunks of at most chunk bytes.
type rangeReadClient struct {
	contentapi.TTRPCContentClient
	data  []byte
	chunk int

	mu       sync.Mutex
	requests []*contentapi.ReadContentRequest
}

func (c *rangeReadClient) Read(ctx context.Context, req *contentapi.ReadContentRequest) (contentapi.TTRPCContent_ReadClient, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	end := int64(len(c.data))
	if req.Size > 0 {
		end = min(end, req.Offset+req.Size)
	}
	var chunks [][]byte
	for b := c.data[req.Offset:end]; len(b) > 0; {
		n := min(len(b), c.chunk)
		chunks = append(chunks, b[:n])
		b = b[n:]
	}
	return &fakeReadStream{chunks: chunks}, nil
}

func TestRemoteReaderWindow(t *testing.T) {
	defer withReadWindow(4)()

	const data = "hello world"
	client := &rangeReadClient{data: []byte(data), chunk: 3}
	ra := newRemoteReaderAt(context.Background(), digest.FromString(data), int64(len(data)), client)
	defer ra.Close()

	r := ra.Reader()
	got := make([]byte, len(data))
	if _, err := io.ReadFull(r, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != data {
		t.Fatalf("read %q, want %q", got, data)
	}
	if n, err := r.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Errorf("Read() at the end = %d, %v, want 0, EOF", n, err)
	}
	// All streams are released once the content has been read.
	if n := len(ra.streams); n != 0 {
		t.Errorf("open streams after reading the content = %d, want 0", n)
	}

	var ranges [][2]int64
	for _, req := range client.requests {
		ranges = append(ranges, [2]int64{req.Offset, req.Size})
	}
	// The content is requested in windows of 4 bytes, the last short.
	if want := [][2]int64{{0, 4}, {4, 4}, {8, 3}}; !slices.Equal(ranges, want) {
		t.Errorf("requested ranges %v, want %v", ranges, want)
	}
}

func TestRemoteReaderReadsAhead(t *testing.T) {
	defer withReadWindow(4)()

	const data = "hello world"
	client := &rangeReadClient{data: []byte(data), chunk: 4}
	ra := newRemoteReaderAt(context.Background(), digest.FromString(data), int64(len(data)), client)
	defer ra.Close()

	r := ra.Reader()
	// The first read opens the first window and requests the next, so two
	// streams are open while the first window is consumed.
	got := make([]byte, 1)
	if _, err := io.ReadFull(r, got); err != nil {
		t.Fatal(err)
	}
	if n := len(ra.streams); n != 2 {
		t.Errorf("open streams while reading the first window = %d, want 2", n)
	}
	client.mu.Lock()
	reqs := len(client.requests)
	client.mu.Unlock()
	if reqs != 2 {
		t.Errorf("requests after the first read = %d, want 2", reqs)
	}

	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+string(rest) != data {
		t.Fatalf("read %q, want %q", string(got)+string(rest), data)
	}
}

func withReadWindow(n int64) func() {
	old := readWindow
	readWindow = n
	return func() { readWindow = old }
}

// delayedReadClient blocks each Read until release is closed, so a test can
// run code while a stream is being opened.
type delayedReadClient struct {
	contentapi.TTRPCContentClient
	entered chan struct{}
	release chan struct{}
	stream  contentapi.TTRPCContent_ReadClient
}

func (c *delayedReadClient) Read(ctx context.Context, req *contentapi.ReadContentRequest) (contentapi.TTRPCContent_ReadClient, error) {
	close(c.entered)
	<-c.release
	return c.stream, nil
}

func TestRemoteReaderAtCloseWhileOpening(t *testing.T) {
	const data = "hello world"
	client := &delayedReadClient{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		stream:  &fakeReadStream{chunks: [][]byte{[]byte(data)}},
	}
	ra := newRemoteReaderAt(context.Background(), digest.FromString(data), int64(len(data)), client)

	opened := make(chan *stream, 1)
	go func() { opened <- ra.open(0) }()

	<-client.entered // open is blocked in client.Read.
	if err := ra.Close(); err != nil {
		t.Fatal(err)
	}
	close(client.release) // let client.Read return.

	s := <-opened
	// A stream which finished opening after Close must not be usable, or it
	// would never be released.
	if _, err := s.read(make([]byte, 1)); !errors.Is(err, errReaderAtClosed) {
		t.Errorf("read() on a stream opened after close = %v, want %v", err, errReaderAtClosed)
	}
	if n := len(ra.streams); n != 0 {
		t.Errorf("streams registered after close = %d, want 0", n)
	}
}

// BenchmarkRead compares reading content through the proxy sequentially, as
// decompression does, when each ReadAt is a Read stream (the previous
// behaviour) and over windowed streams.
func BenchmarkRead(b *testing.B) {
	const size = 64 * 1024 * 1024
	ctx := context.Background()
	root := b.TempDir()
	s := newTestServer(b, root, nil)
	blob, desc := randomBlob(b, size)
	writeBlob(b, s.proxy(b), blob, desc)

	for _, bc := range []struct {
		name string
		// wrap hides optional interfaces of the reader when set.
		wrap bool
	}{
		{name: "ReadAt", wrap: true},
		{name: "Stream"},
	} {
		b.Run(bc.name, func(b *testing.B) {
			cs := s.proxy(b)
			buf := make([]byte, 32*1024)
			b.SetBytes(size)
			b.ResetTimer()
			for b.Loop() {
				ra, err := cs.ReaderAt(ctx, desc)
				if err != nil {
					b.Fatal(err)
				}
				var r io.Reader
				if bc.wrap {
					r = io.NewSectionReader(ra, 0, ra.Size())
				} else {
					r = content.NewReader(ra)
				}
				// Hide io.Discard's ReadFrom, which reads with its own
				// 8KiB buffer, so reads use the 32KiB buffer.
				n, err := io.CopyBuffer(struct{ io.Writer }{io.Discard}, struct{ io.Reader }{r}, buf)
				ra.Close()
				if err != nil {
					b.Fatal(err)
				}
				if n != size {
					b.Fatalf("read %d bytes, expected %d", n, size)
				}
			}
		})
	}
}
