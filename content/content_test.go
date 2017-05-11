package content

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	_ "crypto/sha256" // required for digest package
	"fmt"
	"io"
	"io/ioutil"
	mrand "math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/testutil"
	"github.com/opencontainers/go-digest"
)

func TestContentWriter(t *testing.T) {
	ctx, tmpdir, cs, cleanup := contentStoreEnv(t)
	defer cleanup()
	defer testutil.DumpDir(t, tmpdir)

	if _, err := os.Stat(filepath.Join(tmpdir, "ingest")); os.IsNotExist(err) {
		t.Fatal("ingest dir should be created", err)
	}

	cw, err := cs.Writer(ctx, "myref", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}

	// reopen, so we can test things
	cw, err = cs.Writer(ctx, "myref", 0, "")
	if err != nil {
		t.Fatal(err)
	}

	// make sure that second resume also fails
	if _, err = cs.Writer(ctx, "myref", 0, ""); err == nil {
		// TODO(stevvooe): This also works across processes. Need to find a way
		// to test that, as well.
		t.Fatal("no error on second resume")
	}

	// we should also see this as an active ingestion
	ingestions, err := cs.Status(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	// clear out the time and meta cause we don't care for this test
	for i := range ingestions {
		ingestions[i].UpdatedAt = time.Time{}
		ingestions[i].StartedAt = time.Time{}
	}

	if !reflect.DeepEqual(ingestions, []Status{
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

	checkCopy(t, int64(len(p)), cw, bufio.NewReader(ioutil.NopCloser(bytes.NewReader(p))))

	if err := cw.Commit(int64(len(p)), expected); err != nil {
		t.Fatal(err)
	}

	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}

	cw, err = cs.Writer(ctx, "aref", 0, "")
	if err != nil {
		t.Fatal(err)
	}

	// now, attempt to write the same data again
	checkCopy(t, int64(len(p)), cw, bufio.NewReader(ioutil.NopCloser(bytes.NewReader(p))))
	if err := cw.Commit(int64(len(p)), expected); err != nil {
		t.Fatal(err)
	}

	path := checkBlobPath(t, cs, expected)

	// read the data back, make sure its the same
	pp, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(p, pp) {
		t.Fatal("mismatched data written to disk")
	}

}

func TestWalkBlobs(t *testing.T) {
	ctx, _, cs, cleanup := contentStoreEnv(t)
	defer cleanup()

	const (
		nblobs  = 4 << 10
		maxsize = 4 << 10
	)

	var (
		blobs    = populateBlobStore(t, ctx, cs, nblobs, maxsize)
		expected = map[digest.Digest]struct{}{}
		found    = map[digest.Digest]struct{}{}
	)

	for dgst := range blobs {
		expected[dgst] = struct{}{}
	}

	if err := cs.Walk(ctx, func(bi Info) error {
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
				checkWrite(b, ctx, cs, dgst, p)
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
		p := make([]byte, mrand.Int63n(maxsize))

		if _, err := rand.Read(p); err != nil {
			t.Fatal(err)
		}

		dgst := digest.FromBytes(p)
		blobs[dgst] = p
	}

	return blobs
}

func populateBlobStore(t checker, ctx context.Context, cs Store, nblobs, maxsize int64) map[digest.Digest][]byte {
	blobs := generateBlobs(t, nblobs, maxsize)

	for dgst, p := range blobs {
		checkWrite(t, ctx, cs, dgst, p)
	}

	return blobs
}

func contentStoreEnv(t checker) (context.Context, string, Store, func()) {
	pc, _, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("failed to resolve caller")
	}
	fn := runtime.FuncForPC(pc)

	tmpdir, err := ioutil.TempDir("", filepath.Base(fn.Name())+"-")
	if err != nil {
		t.Fatal(err)
	}

	cs, err := NewStore(tmpdir)
	if err != nil {
		os.RemoveAll(tmpdir)
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return ctx, tmpdir, cs, func() {
		cancel()
		os.RemoveAll(tmpdir)
	}
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

func checkBlobPath(t *testing.T, cs Store, dgst digest.Digest) string {
	path := cs.(*store).blobPath(dgst)

	if path != filepath.Join(cs.(*store).root, "blobs", dgst.Algorithm().String(), dgst.Hex()) {
		t.Fatalf("unexpected path: %q", path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("error stating blob path: %v", err)
	}

	// ensure that only read bits are set.
	if ((fi.Mode() & os.ModePerm) & 0333) != 0 {
		t.Fatalf("incorrect permissions: %v", fi.Mode())
	}

	return path
}

func checkWrite(t checker, ctx context.Context, cs Store, dgst digest.Digest, p []byte) digest.Digest {
	if err := WriteBlob(ctx, cs, dgst.String(), bytes.NewReader(p), int64(len(p)), dgst); err != nil {
		t.Fatal(err)
	}

	return dgst
}
