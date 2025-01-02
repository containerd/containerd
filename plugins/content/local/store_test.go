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

package local

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	_ "crypto/sha256" // required for digest package
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/containerd/errdefs"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/content/testsuite"
	"github.com/containerd/containerd/v2/internal/fsverity"
	"github.com/containerd/containerd/v2/internal/randutil"
	"github.com/containerd/containerd/v2/pkg/testutil"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
)

type memoryLabelStore struct {
	l      sync.Mutex
	labels map[digest.Digest]map[string]string
}

func newMemoryLabelStore() LabelStore {
	return &memoryLabelStore{
		labels: map[digest.Digest]map[string]string{},
	}
}

func (mls *memoryLabelStore) Get(d digest.Digest) (map[string]string, error) {
	mls.l.Lock()
	labels := mls.labels[d]
	mls.l.Unlock()

	return labels, nil
}

func (mls *memoryLabelStore) Set(d digest.Digest, labels map[string]string) error {
	mls.l.Lock()
	mls.labels[d] = labels
	mls.l.Unlock()

	return nil
}

func (mls *memoryLabelStore) Update(d digest.Digest, update map[string]string) (map[string]string, error) {
	mls.l.Lock()
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
	mls.l.Unlock()

	return labels, nil
}

func TestContent(t *testing.T) {
	testsuite.ContentSuite(t, "fs", func(ctx context.Context, root string) (context.Context, content.Store, func() error, error) {
		cs, err := NewLabeledStore(root, newMemoryLabelStore())
		if err != nil {
			return nil, nil, nil, err
		}
		return ctx, cs, func() error {
			return nil
		}, nil
	})
}

func TestContentWriter(t *testing.T) {
	ctx, tmpdir, cs, cleanup := contentStoreEnv(t)
	defer cleanup()
	defer testutil.DumpDirOnFailure(t, tmpdir)

	cw, err := cs.Writer(ctx, content.WithRef("myref"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(tmpdir, "ingest")); os.IsNotExist(err) {
		t.Fatal("ingest dir should be created", err)
	}

	// reopen, so we can test things
	cw, err = cs.Writer(ctx, content.WithRef("myref"))
	if err != nil {
		t.Fatal(err)
	}

	// make sure that second resume also fails
	if _, err = cs.Writer(ctx, content.WithRef("myref")); err == nil {
		// TODO(stevvooe): This also works across processes. Need to find a way
		// to test that, as well.
		t.Fatal("no error on second resume")
	}

	// we should also see this as an active ingestion
	ingestions, err := cs.ListStatuses(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	// clear out the time and meta cause we don't care for this test
	for i := range ingestions {
		ingestions[i].UpdatedAt = time.Time{}
		ingestions[i].StartedAt = time.Time{}
	}

	if !reflect.DeepEqual(ingestions, []content.Status{
		{
			Ref:    "myref",
			Offset: 0,
		},
	}) {
		t.Fatalf("unexpected ingestion set: %v", ingestions)
	}

	p := make([]byte, 4<<20)
	if _, err := rand.Read(p); err != nil {
		t.Fatal(err)
	}
	expected := digest.FromBytes(p)

	checkCopy(t, int64(len(p)), cw, bufio.NewReader(io.NopCloser(bytes.NewReader(p))))

	if err := cw.Commit(ctx, int64(len(p)), expected); err != nil {
		t.Fatal(err)
	}

	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}

	cw, err = cs.Writer(ctx, content.WithRef("aref"))
	if err != nil {
		t.Fatal(err)
	}

	// now, attempt to write the same data again
	checkCopy(t, int64(len(p)), cw, bufio.NewReader(io.NopCloser(bytes.NewReader(p))))
	if err := cw.Commit(ctx, int64(len(p)), expected); err == nil {
		t.Fatal("expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	path := checkBlobPath(t, cs, expected)

	// read the data back, make sure its the same
	pp, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(p, pp) {
		t.Fatal("mismatched data written to disk")
	}

	// ensure fsverity is enabled on blob if fsverity is supported
	ok, err := fsverity.IsSupported(tmpdir)
	if !ok || err != nil {
		t.Log("fsverity not supported, skipping fsverity check")
		return
	}

	ok, err = fsverity.IsEnabled(path)
	if !ok || err != nil {
		t.Fatal(err)
	}

}

func TestWalkBlobs(t *testing.T) {
	ctx, _, cs, cleanup := contentStoreEnv(t)
	defer cleanup()

	const (
		nblobs  = 79
		maxsize = 4 << 10
	)
	var (
		blobs    = populateBlobStore(ctx, t, cs, nblobs, maxsize)
		expected = map[digest.Digest]struct{}{}
		found    = map[digest.Digest]struct{}{}
	)

	for dgst := range blobs {
		expected[dgst] = struct{}{}
	}

	if err := cs.Walk(ctx, func(bi content.Info) error {
		found[bi.Digest] = struct{}{}
		checkBlobPath(t, cs, bi.Digest)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(expected, found) {
		t.Fatalf("expected did not match found: %v != %v", found, expected)
	}
}

// BenchmarkIngests checks the insertion time over varying blob sizes.
//
// Note that at the time of writing there is roughly a 4ms insertion overhead
// for blobs. This seems to be due to the number of syscalls and file io we do
// coordinating the ingestion.
func BenchmarkIngests(b *testing.B) {
	ctx, _, cs, cleanup := contentStoreEnv(b)
	defer cleanup()

	for _, size := range []int64{
		1 << 10,
		4 << 10,
		512 << 10,
		1 << 20,
	} {
		size := size
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.StopTimer()
			blobs := generateBlobs(b, int64(b.N), size)

			var bytes int64
			for _, blob := range blobs {
				bytes += int64(len(blob))
			}
			b.SetBytes(bytes)

			b.StartTimer()

			for dgst, p := range blobs {
				checkWrite(ctx, b, cs, dgst, p)
			}
		})
	}
}

type checker interface {
	Fatal(args ...interface{})
}

func generateBlobs(t checker, nblobs, maxsize int64) map[digest.Digest][]byte {
	blobs := map[digest.Digest][]byte{}

	for i := int64(0); i < nblobs; i++ {
		p := make([]byte, randutil.Int63n(maxsize))

		if _, err := rand.Read(p); err != nil {
			t.Fatal(err)
		}

		dgst := digest.FromBytes(p)
		blobs[dgst] = p
	}

	return blobs
}

func populateBlobStore(ctx context.Context, t checker, cs content.Store, nblobs, maxsize int64) map[digest.Digest][]byte {
	blobs := generateBlobs(t, nblobs, maxsize)

	for dgst, p := range blobs {
		checkWrite(ctx, t, cs, dgst, p)
	}

	return blobs
}

func checkCopy(t checker, size int64, dst io.Writer, src io.Reader) {
	nn, err := io.Copy(dst, src)
	if err != nil {
		t.Fatal(err)
	}

	if nn != size {
		t.Fatal("incorrect number of bytes copied")
	}
}

func checkBlobPath(t *testing.T, cs content.Store, dgst digest.Digest) string {
	path, err := cs.(*store).blobPath(dgst)
	if err != nil {
		t.Fatalf("failed to calculate blob path: %v", err)
	}

	if path != filepath.Join(cs.(*store).root, "blobs", dgst.Algorithm().String(), dgst.Encoded()) {
		t.Fatalf("unexpected path: %q", path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("error stating blob path: %v", err)
	}

	if runtime.GOOS != "windows" {
		// ensure that only read bits are set.
		if ((fi.Mode() & os.ModePerm) & 0333) != 0 {
			t.Fatalf("incorrect permissions: %v", fi.Mode())
		}
	}

	return path
}

func checkWrite(ctx context.Context, t checker, cs content.Store, dgst digest.Digest, p []byte) digest.Digest {
	if err := content.WriteBlob(ctx, cs, dgst.String(), bytes.NewReader(p),
		ocispec.Descriptor{Size: int64(len(p)), Digest: dgst}); err != nil {
		t.Fatal(err)
	}

	return dgst
}

func TestWriterTruncateRecoversFromIncompleteWrite(t *testing.T) {
	cs, err := NewStore(t.TempDir())
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ref := "ref"
	contentB := []byte("this is the content")
	total := int64(len(contentB))
	setupIncompleteWrite(ctx, t, cs, ref, total)

	writer, err := cs.Writer(ctx, content.WithRef(ref), content.WithDescriptor(ocispec.Descriptor{Size: total}))
	assert.NoError(t, err)

	assert.Nil(t, writer.Truncate(0))

	_, err = writer.Write(contentB)
	assert.NoError(t, err)

	dgst := digest.FromBytes(contentB)
	err = writer.Commit(ctx, total, dgst)
	assert.NoError(t, err)
}

func setupIncompleteWrite(ctx context.Context, t *testing.T, cs content.Store, ref string, total int64) {
	writer, err := cs.Writer(ctx, content.WithRef(ref), content.WithDescriptor(ocispec.Descriptor{Size: total}))
	assert.NoError(t, err)

	_, err = writer.Write([]byte("bad data"))
	assert.NoError(t, err)

	assert.Nil(t, writer.Close())
}

func TestWriteReadEmptyFileTimestamp(t *testing.T) {
	root := t.TempDir()

	emptyFile := filepath.Join(root, "updatedat")
	if err := writeTimestampFile(emptyFile, time.Time{}); err != nil {
		t.Errorf("failed to write Zero Time to file: %v", err)
	}

	timestamp, err := readFileTimestamp(emptyFile)
	if err != nil {
		t.Errorf("read empty timestamp file should success, but got error: %v", err)
	}
	if !timestamp.IsZero() {
		t.Errorf("read empty timestamp file should return time.Time{}, but got: %v", timestamp)
	}
}
