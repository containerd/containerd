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
	"cmp"
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
	"time"
	"weak"

	contentapi "github.com/containerd/containerd/api/services/content/v1"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/content/testsuite"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/dialer"
	"github.com/containerd/containerd/v2/pkg/sys"
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

// listenLocal listens on a unix socket in a new temporary directory, as
// containerd listens for its own API. It returns the socket's address and a
// function which removes the directory.
func listenLocal() (net.Listener, string, func(), error) {
	// Keep the socket path short, test names can make t.TempDir long.
	dir, err := os.MkdirTemp("", "csproxy-")
	if err != nil {
		return nil, "", nil, err
	}
	address := filepath.Join(dir, "content.sock")
	l, err := sys.GetLocalListener(address, os.Getuid(), os.Getgid())
	if err != nil {
		os.RemoveAll(dir)
		return nil, "", nil, err
	}
	return l, address, func() { os.RemoveAll(dir) }, nil
}

// startTestServer serves a local content store rooted at root over gRPC on a
// unix socket. It returns a function which stops the server.
func startTestServer(root string, ls local.LabelStore, opts ...grpc.ServerOption) (*testServer, func(), error) {
	backend, err := local.NewLabeledStore(root, ls)
	if err != nil {
		return nil, nil, err
	}
	l, address, removeDir, err := listenLocal()
	if err != nil {
		return nil, nil, err
	}

	s := &testServer{
		backend: backend,
		root:    root,
		address: address,
		streams: map[string]int{},
	}
	srv := grpc.NewServer(append([]grpc.ServerOption{
		grpc.MaxRecvMsgSize(defaults.DefaultMaxRecvMsgSize),
		grpc.MaxSendMsgSize(defaults.DefaultMaxSendMsgSize),
		grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			s.mu.Lock()
			s.streams[info.FullMethod]++
			s.mu.Unlock()
			return handler(srv, ss)
		}),
	}, opts...)...)
	if err := contentserver.New(backend).(interface{ Register(*grpc.Server) error }).Register(srv); err != nil {
		l.Close()
		removeDir()
		return nil, nil, err
	}
	go srv.Serve(l)

	return s, func() {
		srv.Stop()
		removeDir()
	}, nil
}

func newTestServer(t testing.TB, root string, ls local.LabelStore, opts ...grpc.ServerOption) *testServer {
	t.Helper()
	s, stop, err := startTestServer(root, ls, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return s
}

func (s *testServer) streamCount(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[method]
}

// dial returns a proxy content store connected to s the way containerd
// connects to a proxy plugin, and a function which closes the connection.
func (s *testServer) dial(opts ...Opt) (content.Store, func() error, error) {
	conn, err := grpc.NewClient(dialer.DialAddress(s.address),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer.ContextDialer),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(defaults.DefaultMaxRecvMsgSize),
			grpc.MaxCallSendMsgSize(defaults.DefaultMaxSendMsgSize),
		))
	if err != nil {
		return nil, nil, err
	}
	return NewContentStore(conn, opts...), conn.Close, nil
}

func (s *testServer) proxy(t testing.TB, opts ...Opt) content.Store {
	t.Helper()
	cs, closeConn, err := s.dial(opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeConn() })
	return cs
}

// suiteStore returns a content suite factory which serves a store over gRPC
// and returns a proxy to it, reading blobs from the store's directory when
// localBlobs is set. The suite calls the factory from subtests, so it does not
// use the suite's test.
func suiteStore(localBlobs bool) testsuite.StoreInitFn {
	return func(ctx context.Context, root string) (context.Context, content.Store, func() error, error) {
		s, stop, err := startTestServer(root, newMemoryLabelStore())
		if err != nil {
			return ctx, nil, nil, err
		}
		var opts []Opt
		if localBlobs {
			opts = append(opts, WithLocalBlobs(filepath.Join(root, "blobs")))
		}
		cs, closeConn, err := s.dial(opts...)
		if err != nil {
			stop()
			return ctx, nil, nil, err
		}
		return ctx, cs, func() error {
			err := closeConn()
			stop()
			return err
		}, nil
	}
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
	testsuite.ContentSuite(t, "proxy", suiteStore(false))
}

func TestContentSuiteLocalBlobs(t *testing.T) {
	testsuite.ContentSuite(t, "proxy-local-blobs", suiteStore(true))
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

func TestReaderAtLocalBlobs(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s := newTestServer(t, root, nil)
	cs := s.proxy(t, WithLocalBlobs(filepath.Join(root, "blobs")))

	b, desc := randomBlob(t, 3*1024*1024+17)
	writeBlob(t, cs, b, desc)

	ra, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		t.Fatal(err)
	}
	defer ra.Close()
	if _, ok := ra.(*localReaderAt); !ok {
		t.Fatalf("expected a local reader, got %T", ra)
	}
	checkRead(t, ra, b)
	if n := s.streamCount(readMethod); n != 0 {
		t.Fatalf("expected no read streams, got %d", n)
	}
}

func TestReaderAtLocalBlobsFallback(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		// prepare modifies the local blobs directory after the blob is
		// written to the store rooted at storeRoot.
		prepare func(t *testing.T, localBlobs, storeRoot string, desc ocispec.Descriptor)
		// level is the level at which the fallback is logged.
		level logrus.Level
	}{
		{
			name:    "Missing",
			prepare: func(*testing.T, string, string, ocispec.Descriptor) {},
			level:   logrus.DebugLevel,
		},
		{
			name: "SizeMismatch",
			prepare: func(t *testing.T, localBlobs, _ string, desc ocispec.Descriptor) {
				p, err := localBlobPath(localBlobs, desc.Digest)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("truncated"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			level: logrus.WarnLevel,
		},
		{
			name: "NotRegular",
			prepare: func(t *testing.T, localBlobs, _ string, desc ocispec.Descriptor) {
				p, err := localBlobPath(localBlobs, desc.Digest)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(p, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			level: logrus.WarnLevel,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storeRoot := t.TempDir()
			localBlobs := t.TempDir()
			s := newTestServer(t, storeRoot, nil)
			cs := s.proxy(t, WithLocalBlobs(localBlobs))

			b, desc := randomBlob(t, 2*1024*1024+5)
			writeBlob(t, cs, b, desc)
			tc.prepare(t, localBlobs, storeRoot, desc)

			logger := logrus.New()
			logger.SetOutput(io.Discard)
			logger.SetLevel(logrus.DebugLevel)
			hook := &levelHook{}
			logger.AddHook(hook)
			ctx := log.WithLogger(ctx, logrus.NewEntry(logger))

			ra, err := cs.ReaderAt(ctx, desc)
			if err != nil {
				t.Fatal(err)
			}
			defer ra.Close()
			if _, ok := ra.(*remoteReaderAt); !ok {
				t.Fatalf("expected a remote reader, got %T", ra)
			}
			if !slices.Equal(hook.levels, []logrus.Level{tc.level}) {
				t.Errorf("fallback logged at levels %v, want [%v]", hook.levels, tc.level)
			}
			checkRead(t, ra, b)
		})
	}
}

// levelHook records the level of each log entry.
type levelHook struct {
	levels []logrus.Level
}

func (h *levelHook) Levels() []logrus.Level { return logrus.AllLevels }

func (h *levelHook) Fire(e *logrus.Entry) error {
	h.levels = append(h.levels, e.Level)
	return nil
}

func TestReaderAtLocalBlobsNotFound(t *testing.T) {
	root := t.TempDir()
	localBlobs := t.TempDir()
	s := newTestServer(t, root, nil)
	cs := s.proxy(t, WithLocalBlobs(localBlobs))

	_, desc := randomBlob(t, 10)
	// The remote store decides what exists, even when the file is present.
	p, err := localBlobPath(localBlobs, desc.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ReaderAt(context.Background(), desc); !errdefs.IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestLocalBlobPathInvalid(t *testing.T) {
	for _, d := range []digest.Digest{
		"",
		"sha256:../../etc/passwd",
		"sha256:abc",
		digest.Digest("unknown:" + digest.FromString("x").Encoded()),
		digest.Digest("sha256:" + digest.FromString("x").Encoded() + "/.."),
	} {
		if p, err := localBlobPath("/root", d); err == nil {
			t.Errorf("expected error for %q, got path %q", d, p)
		}
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
	// ended, when set, counts streams received to their end.
	ended *atomic.Int32
}

func (s *fakeReadStream) Recv() (*contentapi.ReadContentResponse, error) {
	if len(s.chunks) == 0 {
		if s.ended != nil {
			s.ended.Add(1)
			s.ended = nil
		}
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

	// ended counts streams received to their end.
	ended atomic.Int32
}

// requested reports the number of streams requested.
func (c *rangeReadClient) requested() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

// open reports the number of streams not received to their end. A ttrpc
// client keeps such a stream registered for the life of the connection.
func (c *rangeReadClient) open() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests) - int(c.ended.Load())
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
	return &fakeReadStream{chunks: chunks, ended: &c.ended}, nil
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
	client.mu.Lock()
	for _, req := range client.requests {
		ranges = append(ranges, [2]int64{req.Offset, req.Size})
	}
	client.mu.Unlock()
	// Streams are opened concurrently, so requests arrive in any order.
	slices.SortFunc(ranges, func(a, b [2]int64) int { return cmp.Compare(a[0], b[0]) })
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
	// Streams are opened in the background.
	deadline := time.Now().Add(5 * time.Second)
	for client.requested() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := client.requested(); n != 2 {
		t.Errorf("requests after the first read = %d, want 2", n)
	}

	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+string(rest) != data {
		t.Fatalf("read %q, want %q", string(got)+string(rest), data)
	}
}

func TestRemoteReaderReceivesStreamsToEnd(t *testing.T) {
	defer withReadWindow(4)()

	const data = "hello world, hello world"
	for _, tc := range []struct {
		name string
		// read reads from r, then ends reading by closing ra or cancelling
		// the context of ra.
		read func(t *testing.T, r io.Reader, ra *remoteReaderAt, cancel context.CancelFunc)
	}{
		{
			name: "Complete",
			read: func(t *testing.T, r io.Reader, _ *remoteReaderAt, _ context.CancelFunc) {
				if _, err := io.ReadAll(r); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "Close",
			read: func(t *testing.T, r io.Reader, ra *remoteReaderAt, _ context.CancelFunc) {
				if _, err := r.Read(make([]byte, 1)); err != nil {
					t.Fatal(err)
				}
				ra.Close()
			},
		},
		{
			name: "Cancel",
			read: func(t *testing.T, r io.Reader, _ *remoteReaderAt, cancel context.CancelFunc) {
				if _, err := r.Read(make([]byte, 1)); err != nil {
					t.Fatal(err)
				}
				cancel()
				if _, err := io.ReadAll(r); !errors.Is(err, context.Canceled) {
					t.Fatalf("io.ReadAll() after cancel = %v, want %v", err, context.Canceled)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &rangeReadClient{data: []byte(data), chunk: 1}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ra := newRemoteReaderAt(ctx, digest.FromString(data), int64(len(data)), client)
			defer ra.Close()

			tc.read(t, ra.Reader(), ra, cancel)

			// Released streams are received to their end in the
			// background.
			deadline := time.Now().Add(5 * time.Second)
			for client.open() > 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if n := client.open(); n != 0 {
				t.Errorf("streams not received to their end = %d, want 0", n)
			}
		})
	}
}

// heldReadClient serves a single response, then holds back the final message
// of the stream until release is closed.
type heldReadClient struct {
	contentapi.TTRPCContentClient
	data    []byte
	release chan struct{}
}

func (c *heldReadClient) Read(ctx context.Context, req *contentapi.ReadContentRequest) (contentapi.TTRPCContent_ReadClient, error) {
	data := c.data
	c.data = nil
	return &heldReadStream{data: data, release: c.release}, nil
}

type heldReadStream struct {
	ttrpc.ClientStream
	data    []byte
	release chan struct{}
}

func (s *heldReadStream) Recv() (*contentapi.ReadContentResponse, error) {
	if s.data != nil {
		resp := &contentapi.ReadContentResponse{Data: s.data}
		s.data = nil
		return resp, nil
	}
	<-s.release
	return nil, io.EOF
}

func TestStreamReleasesReadContent(t *testing.T) {
	const size = 1 << 20
	data := make([]byte, size)
	content := weak.Make(&data[0])
	client := &heldReadClient{data: data, release: make(chan struct{})}
	defer close(client.release)
	data = nil

	ra := newRemoteReaderAt(context.Background(), digest.FromString("data"), size, client)
	s := ra.open(0)
	if _, err := io.ReadFull(readerFunc(s.read), make([]byte, size)); err != nil {
		t.Fatal(err)
	}
	// The stream stays referenced until its final message arrives, but the
	// content it has passed to read does not.
	s.release()
	runtime.GC()
	if content.Value() != nil {
		t.Error("content read from a released stream is still referenced")
	}
	runtime.KeepAlive(s)
}

type readerFunc func(p []byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestRemoteReaderReadAfterRelease(t *testing.T) {
	const data = "hello world"
	for _, tc := range []struct {
		name string
		// end ends reading by closing ra or cancelling the context of ra.
		end     func(ra *remoteReaderAt, cancel context.CancelFunc)
		wantErr error
	}{
		{
			name:    "Close",
			end:     func(ra *remoteReaderAt, _ context.CancelFunc) { ra.Close() },
			wantErr: errReaderAtClosed,
		},
		{
			name:    "Cancel",
			end:     func(_ *remoteReaderAt, cancel context.CancelFunc) { cancel() },
			wantErr: context.Canceled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The content arrives in a single response, so the rest of it is
			// already received when reading ends.
			client := &rangeReadClient{data: []byte(data), chunk: len(data)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ra := newRemoteReaderAt(ctx, digest.FromString(data), int64(len(data)), client)
			defer ra.Close()

			r := ra.Reader()
			if _, err := io.ReadFull(r, make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
			tc.end(ra, cancel)
			if got, err := io.ReadAll(r); !errors.Is(err, tc.wantErr) || len(got) != 0 {
				t.Errorf("io.ReadAll() after %s = %q, %v, want \"\", %v", tc.name, got, err, tc.wantErr)
			}
		})
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
	stream  *fakeReadStream
}

func (c *delayedReadClient) Read(ctx context.Context, req *contentapi.ReadContentRequest) (contentapi.TTRPCContent_ReadClient, error) {
	close(c.entered)
	<-c.release
	return c.stream, nil
}

func TestRemoteReaderAtCloseWhileOpening(t *testing.T) {
	const data = "hello world"
	var ended atomic.Int32
	client := &delayedReadClient{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		stream:  &fakeReadStream{chunks: [][]byte{[]byte(data)}, ended: &ended},
	}
	ra := newRemoteReaderAt(context.Background(), digest.FromString(data), int64(len(data)), client)

	s := ra.open(0)
	<-client.entered // The stream is being opened.
	if err := ra.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.read(make([]byte, 1)); !errors.Is(err, errReaderAtClosed) {
		t.Errorf("read() after close = %v, want %v", err, errReaderAtClosed)
	}
	if n := len(ra.streams); n != 0 {
		t.Errorf("streams registered after close = %d, want 0", n)
	}

	// The stream finishes opening and is received to its end.
	close(client.release)
	deadline := time.Now().Add(5 * time.Second)
	for ended.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if ended.Load() == 0 {
		t.Error("stream opened after close was not received to its end")
	}
}

// stallReadClient serves Read streams which neither open nor send until their
// context is cancelled, and closes cancelled when it is.
type stallReadClient struct {
	contentapi.TTRPCContentClient
	stallOpen bool
	cancelled chan struct{}
}

func (c *stallReadClient) Read(ctx context.Context, req *contentapi.ReadContentRequest) (contentapi.TTRPCContent_ReadClient, error) {
	if c.stallOpen {
		<-ctx.Done()
		close(c.cancelled)
		return nil, ctx.Err()
	}
	return &stallReadStream{ctx: ctx, cancelled: c.cancelled}, nil
}

type stallReadStream struct {
	ttrpc.ClientStream
	ctx       context.Context
	cancelled chan struct{}
}

func (s *stallReadStream) Recv() (*contentapi.ReadContentResponse, error) {
	<-s.ctx.Done()
	close(s.cancelled)
	return nil, s.ctx.Err()
}

func TestRemoteReaderAtReleaseTimeout(t *testing.T) {
	defer withReleaseTimeout(10 * time.Millisecond)()

	for _, tc := range []struct {
		name      string
		stallOpen bool
	}{
		{name: "Open", stallOpen: true},
		{name: "Receive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &stallReadClient{stallOpen: tc.stallOpen, cancelled: make(chan struct{})}
			ra := newRemoteReaderAt(context.Background(), digest.FromString("data"), 4, client)
			s := ra.open(0)

			// A stream which is not released is not cancelled, however slow.
			select {
			case <-client.cancelled:
				t.Fatal("RPC of an open stream was cancelled")
			case <-time.After(10 * releaseTimeout):
			}

			s.release()
			select {
			case <-client.cancelled:
			case <-time.After(5 * time.Second):
				t.Fatal("RPC of a released stream was not cancelled")
			}
		})
	}
}

func withReleaseTimeout(d time.Duration) func() {
	old := releaseTimeout
	releaseTimeout = d
	return func() { releaseTimeout = old }
}

// readWithin reads all of r, failing the test if it takes longer than d.
func readWithin(t *testing.T, r io.Reader, d time.Duration) []byte {
	t.Helper()
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(r)
		done <- result{b, err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatal(res.err)
		}
		return res.b
	case <-time.After(d):
		t.Fatalf("reading did not complete within %v", d)
		return nil
	}
}

func TestRemoteReaderStreamLimit(t *testing.T) {
	// With a server limit of one stream, the window read ahead can only be
	// opened once the current window's stream has ended.
	s := newTestServer(t, t.TempDir(), nil, grpc.MaxConcurrentStreams(1))
	cs := s.proxy(t)

	b, desc := randomBlob(t, int(3*readWindow+5))
	writeBlob(t, cs, b, desc)

	ra, err := cs.ReaderAt(context.Background(), desc)
	if err != nil {
		t.Fatal(err)
	}
	defer ra.Close()
	if got := readWithin(t, content.NewReader(ra), 30*time.Second); !bytes.Equal(got, b) {
		t.Fatal("unexpected content")
	}
}

// ttrpcContentService serves content from a store over ttrpc, sending read
// content in chunks of at most chunk bytes.
type ttrpcContentService struct {
	contentapi.TTRPCContentService
	cs    content.Store
	chunk int
}

func (s *ttrpcContentService) Info(ctx context.Context, req *contentapi.InfoRequest) (*contentapi.InfoResponse, error) {
	info, err := s.cs.Info(ctx, digest.Digest(req.Digest))
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	return &contentapi.InfoResponse{Info: infoToGRPC(&info)}, nil
}

func (s *ttrpcContentService) Read(ctx context.Context, req *contentapi.ReadContentRequest, srv contentapi.TTRPCContent_ReadServer) error {
	ra, err := s.cs.ReaderAt(ctx, ocispec.Descriptor{Digest: digest.Digest(req.Digest)})
	if err != nil {
		return errgrpc.ToGRPC(err)
	}
	defer ra.Close()
	end := ra.Size()
	if req.Size > 0 {
		end = min(end, req.Offset+req.Size)
	}
	p := make([]byte, s.chunk)
	for off := req.Offset; off < end; {
		n := min(int64(len(p)), end-off)
		if _, err := ra.ReadAt(p[:n], off); err != nil && err != io.EOF {
			return errgrpc.ToGRPC(err)
		}
		if err := srv.Send(&contentapi.ReadContentResponse{Offset: off, Data: p[:n]}); err != nil {
			return err
		}
		off += n
	}
	return nil
}

func TestRemoteReaderSlowConsumerTTRPC(t *testing.T) {
	backend, err := local.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, desc := randomBlob(t, int(2*readWindow))
	writeBlob(t, backend, b, desc)

	l, address, removeDir, err := listenLocal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(removeDir)
	srv, err := ttrpc.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	// Small responses take more messages per window than a ttrpc stream
	// queues.
	contentapi.RegisterTTRPCContentService(srv, &ttrpcContentService{cs: backend, chunk: 8 * 1024})
	go srv.Serve(context.Background(), l)
	t.Cleanup(func() { srv.Close() })

	// The dialer treats a context without a deadline as a zero timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := dialer.ContextDialer(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	client := ttrpc.NewClient(conn)
	t.Cleanup(func() { client.Close() })
	cs := NewContentStore(client)

	ra, err := cs.ReaderAt(context.Background(), desc)
	if err != nil {
		t.Fatal(err)
	}
	defer ra.Close()
	r := content.NewReader(ra)
	first := make([]byte, 1)
	if _, err := io.ReadFull(r, first); err != nil {
		t.Fatal(err)
	}
	// ttrpc closes a stream whose queue stays full for a second.
	time.Sleep(1500 * time.Millisecond)
	rest := readWithin(t, r, 30*time.Second)
	if !bytes.Equal(append(first, rest...), b) {
		t.Fatal("unexpected content")
	}
}

// BenchmarkRead compares reading content through the proxy sequentially, as
// decompression does, when each ReadAt is a Read stream (the previous
// behaviour), over windowed streams, and from local blobs.
func BenchmarkRead(b *testing.B) {
	const size = 64 * 1024 * 1024
	ctx := context.Background()
	root := b.TempDir()
	s := newTestServer(b, root, nil)
	blob, desc := randomBlob(b, size)
	writeBlob(b, s.proxy(b), blob, desc)

	for _, bc := range []struct {
		name string
		opts []Opt
		// wrap hides optional interfaces of the reader when set.
		wrap bool
	}{
		{name: "ReadAt", wrap: true},
		{name: "Stream"},
		{name: "LocalBlobs", opts: []Opt{WithLocalBlobs(filepath.Join(root, "blobs"))}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			cs := s.proxy(b, bc.opts...)
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
