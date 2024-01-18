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

package testsuite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/internal/testutil"
	"github.com/containerd/containerd/v2/pkg/errdefs"
	"github.com/containerd/log/logtest"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
)

const (
	emptyDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// StoreInitFn initializes content store with given root and returns a function for
// destroying the content store
type StoreInitFn func(ctx context.Context, root string) (context.Context, content.Store, func() error, error)

// ContentSuite runs a test suite on the content store given a factory function.
func ContentSuite(t *testing.T, name string, storeFn StoreInitFn) {
	t.Run("Writer", makeTest(t, name, storeFn, checkContentStoreWriter))
	t.Run("UpdateStatus", makeTest(t, name, storeFn, checkUpdateStatus))
	t.Run("CommitExists", makeTest(t, name, storeFn, checkCommitExists))
	t.Run("Resume", makeTest(t, name, storeFn, checkResumeWriter))
	t.Run("ResumeTruncate", makeTest(t, name, storeFn, checkResume(resumeTruncate)))
	t.Run("ResumeDiscard", makeTest(t, name, storeFn, checkResume(resumeDiscard)))
	t.Run("ResumeCopy", makeTest(t, name, storeFn, checkResume(resumeCopy)))
	t.Run("ResumeCopySeeker", makeTest(t, name, storeFn, checkResume(resumeCopySeeker)))
	t.Run("ResumeCopyReaderAt", makeTest(t, name, storeFn, checkResume(resumeCopyReaderAt)))
	t.Run("SmallBlob", makeTest(t, name, storeFn, checkSmallBlob))
	t.Run("Labels", makeTest(t, name, storeFn, checkLabels))

	t.Run("CommitErrorState", makeTest(t, name, storeFn, checkCommitErrorState))
}

// ContentCrossNSSharedSuite runs a test suite under shared content policy
func ContentCrossNSSharedSuite(t *testing.T, name string, storeFn StoreInitFn) {
	t.Run("CrossNamespaceAppend", makeTest(t, name, storeFn, checkCrossNSAppend))
	t.Run("CrossNamespaceShare", makeTest(t, name, storeFn, checkCrossNSShare))
}

// ContentCrossNSIsolatedSuite runs a test suite under isolated content policy
func ContentCrossNSIsolatedSuite(t *testing.T, name string, storeFn StoreInitFn) {
	t.Run("CrossNamespaceIsolate", makeTest(t, name, storeFn, checkCrossNSIsolate))
}

// ContentSharedNSIsolatedSuite runs a test suite for shared namespaces under isolated content policy
func ContentSharedNSIsolatedSuite(t *testing.T, name string, storeFn StoreInitFn) {
	t.Run("SharedNamespaceIsolate", makeTest(t, name, storeFn, checkSharedNSIsolate))
}

// ContextWrapper is used to decorate new context used inside the test
// before using the context on the content store.
// This can be used to support leasing and multiple namespaces tests.
type ContextWrapper func(ctx context.Context, sharedNS bool) (context.Context, func(context.Context) error, error)

type wrapperKey struct{}

// SetContextWrapper sets the wrapper on the context for deriving
// new test contexts from the context.
func SetContextWrapper(ctx context.Context, w ContextWrapper) context.Context {
	return context.WithValue(ctx, wrapperKey{}, w)
}

type nameKey struct{}

// Name gets the test name from the context
func Name(ctx context.Context) string {
	name, ok := ctx.Value(nameKey{}).(string)
	if !ok {
		return ""
	}
	return name
}

func makeTest(t *testing.T, name string, storeFn func(ctx context.Context, root string) (context.Context, content.Store, func() error, error), fn func(ctx context.Context, t *testing.T, cs content.Store)) func(t *testing.T) {
	return func(t *testing.T) {
		ctx := context.WithValue(context.Background(), nameKey{}, name)
		ctx = logtest.WithT(ctx, t)

		tmpDir, err := os.MkdirTemp("", "content-suite-"+name+"-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		ctx, cs, cleanup, err := storeFn(ctx, tmpDir)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := cleanup(); err != nil && !t.Failed() {
				t.Fatalf("Cleanup failed: %+v", err)
			}
		}()

		w, ok := ctx.Value(wrapperKey{}).(ContextWrapper)
		if ok {
			var done func(context.Context) error
			ctx, done, err = w(ctx, false)
			if err != nil {
				t.Fatalf("Error wrapping context: %+v", err)
			}
			defer func() {
				if err := done(ctx); err != nil && !t.Failed() {
					t.Fatalf("Wrapper release failed: %+v", err)
				}
			}()
		}

		defer testutil.DumpDirOnFailure(t, tmpDir)
		fn(ctx, t, cs)
	}
}

var labels = map[string]string{
	"containerd.io/gc.root": time.Now().UTC().Format(time.RFC3339),
}

func checkContentStoreWriter(ctx context.Context, t *testing.T, cs content.Store) {
	c1, d1 := createContent(256)
	w1, err := content.OpenWriter(ctx, cs, content.WithRef("c1"))
	if err != nil {
		t.Fatal(err)
	}
	defer w1.Close()

	c2, d2 := createContent(256)
	w2, err := content.OpenWriter(ctx, cs, content.WithRef("c2"), content.WithDescriptor(ocispec.Descriptor{Size: int64(len(c2))}))
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	c3, d3 := createContent(256)
	w3, err := content.OpenWriter(ctx, cs, content.WithRef("c3"), content.WithDescriptor(ocispec.Descriptor{Digest: d3}))
	if err != nil {
		t.Fatal(err)
	}
	defer w3.Close()

	c4, d4 := createContent(256)
	w4, err := content.OpenWriter(ctx, cs, content.WithRef("c4"), content.WithDescriptor(ocispec.Descriptor{Size: int64(len(c4)), Digest: d4}))
	if err != nil {
		t.Fatal(err)
	}
	defer w4.Close()

	smallbuf := make([]byte, 32)
	for _, s := range []struct {
		content []byte
		digest  digest.Digest
		writer  content.Writer
	}{
		{
			content: c1,
			digest:  d1,
			writer:  w1,
		},
		{
			content: c2,
			digest:  d2,
			writer:  w2,
		},
		{
			content: c3,
			digest:  d3,
			writer:  w3,
		},
		{
			content: c4,
			digest:  d4,
			writer:  w4,
		},
	} {
		n, err := io.CopyBuffer(s.writer, bytes.NewReader(s.content), smallbuf)
		if err != nil {
			t.Fatal(err)
		}

		if n != int64(len(s.content)) {
			t.Fatalf("Unexpected copy length %d, expected %d", n, len(s.content))
		}

		preCommit := time.Now()
		if err := s.writer.Commit(ctx, 0, "", content.WithLabels(labels)); err != nil {
			t.Fatal(err)
		}
		postCommit := time.Now()

		if s.writer.Digest() != s.digest {
			t.Fatalf("Unexpected commit digest %s, expected %s", s.writer.Digest(), s.digest)
		}

		info := content.Info{
			Digest: s.digest,
			Size:   int64(len(s.content)),
			Labels: labels,
		}
		if err := checkInfo(ctx, cs, s.digest, info, preCommit, postCommit, preCommit, postCommit); err != nil {
			t.Fatalf("Check info failed: %+v", err)
		}
	}
}

func checkResumeWriter(ctx context.Context, t *testing.T, cs content.Store) {
	checkWrite := func(t *testing.T, w io.Writer, p []byte) {
		t.Helper()
		n, err := w.Write(p)
		if err != nil {
			t.Fatal(err)
		}

		if n != len(p) {
			t.Fatal("short write to content store")
		}
	}

	var (
		ref           = "cb"
		cb, dgst      = createContent(256)
		first, second = cb[:128], cb[128:]
	)

	preStart := time.Now()
	w1, err := content.OpenWriter(ctx, cs, content.WithRef(ref), content.WithDescriptor(ocispec.Descriptor{Size: 256, Digest: dgst}))
	if err != nil {
		t.Fatal(err)
	}
	postStart := time.Now()
	preUpdate := postStart

	checkWrite(t, w1, first)
	postUpdate := time.Now()

	dgstFirst := digest.FromBytes(first)
	expected := content.Status{
		Ref:      ref,
		Offset:   int64(len(first)),
		Total:    int64(len(cb)),
		Expected: dgstFirst,
	}

	checkStatus(t, w1, expected, dgstFirst, preStart, postStart, preUpdate, postUpdate)
	assert.Nil(t, w1.Close(), "close first writer")

	w2, err := content.OpenWriter(ctx, cs, content.WithRef(ref), content.WithDescriptor(ocispec.Descriptor{Size: 256, Digest: dgst}))
	if err != nil {
		t.Fatal(err)
	}

	// status should be consistent with version before close.
	checkStatus(t, w2, expected, dgstFirst, preStart, postStart, preUpdate, postUpdate)

	preUpdate = time.Now()
	checkWrite(t, w2, second)
	postUpdate = time.Now()

	expected.Offset = expected.Total
	expected.Expected = dgst
	checkStatus(t, w2, expected, dgst, preStart, postStart, preUpdate, postUpdate)

	preCommit := time.Now()
	if err := w2.Commit(ctx, 0, ""); err != nil {
		t.Fatalf("commit failed: %+v", err)
	}
	postCommit := time.Now()

	assert.Nil(t, w2.Close(), "close second writer")
	info := content.Info{
		Digest: dgst,
		Size:   256,
	}

	if err := checkInfo(ctx, cs, dgst, info, preCommit, postCommit, preCommit, postCommit); err != nil {
		t.Fatalf("Check info failed: %+v", err)
	}
}

func checkCommitExists(ctx context.Context, t *testing.T, cs content.Store) {
	c1, d1 := createContent(256)
	if err := content.WriteBlob(ctx, cs, "c1", bytes.NewReader(c1), ocispec.Descriptor{Digest: d1}); err != nil {
		t.Fatal(err)
	}

	for i, tc := range []struct {
		expected digest.Digest
	}{
		{
			expected: d1,
		},
		{},
	} {
		w, err := content.OpenWriter(ctx, cs, content.WithRef(fmt.Sprintf("c1-commitexists-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(c1); err != nil {
			w.Close()
			t.Fatal(err)
		}
		err = w.Commit(ctx, int64(len(c1)), tc.expected)
		w.Close()
		if err == nil {
			t.Errorf("(%d) Expected already exists error", i)
		} else if !errdefs.IsAlreadyExists(err) {
			t.Fatalf("(%d) Unexpected error: %+v", i, err)
		}
	}
}

func checkRefNotAvailable(ctx context.Context, t *testing.T, cs content.Store, ref string) {
	t.Helper()

	w, err := cs.Writer(ctx, content.WithRef(ref))
	if err == nil {
		defer w.Close()
		t.Fatal("writer created with ref, expected to be in use")
	}
	if !errdefs.IsUnavailable(err) {
		t.Fatalf("Expected unavailable error, got %+v", err)
	}
}

func checkCommitErrorState(ctx context.Context, t *testing.T, cs content.Store) {
	c1, d1 := createContent(256)
	_, d2 := createContent(256)
	if err := content.WriteBlob(ctx, cs, "c1", bytes.NewReader(c1), ocispec.Descriptor{Digest: d1}); err != nil {
		t.Fatal(err)
	}

	ref := "c1-commiterror-state"
	w, err := content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(c1); err != nil {
		if err := w.Close(); err != nil {
			t.Errorf("Close error: %+v", err)
		}
		t.Fatal(err)
	}

	checkRefNotAvailable(ctx, t, cs, ref)

	// Check exists
	err = w.Commit(ctx, int64(len(c1)), d1)
	if err == nil {
		t.Fatalf("Expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		if err := w.Close(); err != nil {
			t.Errorf("Close error: %+v", err)
		}
		t.Fatalf("Unexpected error: %+v", err)
	}

	w, err = content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		t.Fatal(err)
	}

	checkRefNotAvailable(ctx, t, cs, ref)

	if _, err := w.Write(c1); err != nil {
		if err := w.Close(); err != nil {
			t.Errorf("close error: %+v", err)
		}
		t.Fatal(err)
	}

	// Check exists without providing digest
	err = w.Commit(ctx, int64(len(c1)), "")
	if err == nil {
		t.Fatalf("Expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		if err := w.Close(); err != nil {
			t.Errorf("Close error: %+v", err)
		}
		t.Fatalf("Unexpected error: %+v", err)
	}
	w.Close()

	w, err = content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		t.Fatal(err)
	}

	checkRefNotAvailable(ctx, t, cs, ref)

	if _, err := w.Write(append(c1, []byte("more")...)); err != nil {
		if err := w.Close(); err != nil {
			t.Errorf("close error: %+v", err)
		}
		t.Fatal(err)
	}

	// Commit with the wrong digest should produce an error
	err = w.Commit(ctx, int64(len(c1))+4, d2)
	if err == nil {
		t.Fatalf("Expected error from wrong digest")
	} else if !errdefs.IsFailedPrecondition(err) {
		t.Errorf("Unexpected error: %+v", err)
	}

	w.Close()
	w, err = content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		t.Fatal(err)
	}

	checkRefNotAvailable(ctx, t, cs, ref)

	// Commit with wrong size should also produce an error
	err = w.Commit(ctx, int64(len(c1)), "")
	if err == nil {
		t.Fatalf("Expected error from wrong size")
	} else if !errdefs.IsFailedPrecondition(err) {
		t.Errorf("Unexpected error: %+v", err)
	}

	w.Close()
	w, err = content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		t.Fatal(err)
	}

	checkRefNotAvailable(ctx, t, cs, ref)

	// Now expect commit to succeed
	if err := w.Commit(ctx, int64(len(c1))+4, ""); err != nil {
		if err := w.Close(); err != nil {
			t.Errorf("close error: %+v", err)
		}
		t.Fatalf("Failed to commit: %+v", err)
	}

	w.Close()
	// Create another writer with same reference
	w, err = content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		t.Fatalf("Failed to open writer: %+v", err)
	}

	if _, err := w.Write(c1); err != nil {
		if err := w.Close(); err != nil {
			t.Errorf("close error: %+v", err)
		}
		t.Fatal(err)
	}

	checkRefNotAvailable(ctx, t, cs, ref)

	// Commit should fail due to already exists
	err = w.Commit(ctx, int64(len(c1)), d1)
	if err == nil {
		t.Fatalf("Expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		if err := w.Close(); err != nil {
			t.Errorf("close error: %+v", err)
		}
		t.Fatalf("Unexpected error: %+v", err)
	}

	w.Close()
	w, err = content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		t.Fatal(err)
	}

	checkRefNotAvailable(ctx, t, cs, ref)

	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %+v", err)
	}

	// Create another writer with same reference to check available
	w, err = content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		t.Fatalf("Failed to open writer: %+v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %+v", err)
	}
}

func checkUpdateStatus(ctx context.Context, t *testing.T, cs content.Store) {
	c1, d1 := createContent(256)

	preStart := time.Now()
	w1, err := content.OpenWriter(ctx, cs, content.WithRef("c1"), content.WithDescriptor(ocispec.Descriptor{Size: 256, Digest: d1}))
	if err != nil {
		t.Fatal(err)
	}
	defer w1.Close()
	postStart := time.Now()

	d := digest.FromBytes([]byte{})

	expected := content.Status{
		Ref:      "c1",
		Total:    256,
		Expected: d1,
	}
	preUpdate := preStart
	postUpdate := postStart

	checkStatus(t, w1, expected, d, preStart, postStart, preUpdate, postUpdate)

	// Write first 64 bytes
	preUpdate = time.Now()
	if _, err := w1.Write(c1[:64]); err != nil {
		t.Fatalf("Failed to write: %+v", err)
	}
	postUpdate = time.Now()
	expected.Offset = 64
	d = digest.FromBytes(c1[:64])
	checkStatus(t, w1, expected, d, preStart, postStart, preUpdate, postUpdate)

	// Write next 128 bytes
	preUpdate = time.Now()
	if _, err := w1.Write(c1[64:192]); err != nil {
		t.Fatalf("Failed to write: %+v", err)
	}
	postUpdate = time.Now()
	expected.Offset = 192
	d = digest.FromBytes(c1[:192])
	checkStatus(t, w1, expected, d, preStart, postStart, preUpdate, postUpdate)

	// Write last 64 bytes
	preUpdate = time.Now()
	if _, err := w1.Write(c1[192:]); err != nil {
		t.Fatalf("Failed to write: %+v", err)
	}
	postUpdate = time.Now()
	expected.Offset = 256
	checkStatus(t, w1, expected, d1, preStart, postStart, preUpdate, postUpdate)

	preCommit := time.Now()
	if err := w1.Commit(ctx, 0, "", content.WithLabels(labels)); err != nil {
		t.Fatalf("Commit failed: %+v", err)
	}
	postCommit := time.Now()

	info := content.Info{
		Digest: d1,
		Size:   256,
		Labels: labels,
	}

	if err := checkInfo(ctx, cs, d1, info, preCommit, postCommit, preCommit, postCommit); err != nil {
		t.Fatalf("Check info failed: %+v", err)
	}
}

func checkLabels(ctx context.Context, t *testing.T, cs content.Store) {
	c1, d1 := createContent(256)

	w1, err := content.OpenWriter(ctx, cs, content.WithRef("c1-checklabels"), content.WithDescriptor(ocispec.Descriptor{Size: 256, Digest: d1}))
	if err != nil {
		t.Fatal(err)
	}
	defer w1.Close()

	if _, err := w1.Write(c1); err != nil {
		t.Fatalf("Failed to write: %+v", err)
	}

	rootTime := time.Now().UTC().Format(time.RFC3339)
	labels := map[string]string{
		"k1": "v1",
		"k2": "v2",

		"containerd.io/gc.root": rootTime,
	}

	preCommit := time.Now()
	if err := w1.Commit(ctx, 0, "", content.WithLabels(labels)); err != nil {
		t.Fatalf("Commit failed: %+v", err)
	}
	postCommit := time.Now()

	info := content.Info{
		Digest: d1,
		Size:   256,
		Labels: labels,
	}

	if err := checkInfo(ctx, cs, d1, info, preCommit, postCommit, preCommit, postCommit); err != nil {
		t.Fatalf("Check info failed: %+v", err)
	}

	labels["k1"] = "newvalue"
	delete(labels, "k2")
	labels["k3"] = "v3"

	info.Labels = labels
	preUpdate := time.Now()
	if _, err := cs.Update(ctx, info); err != nil {
		t.Fatalf("Update failed: %+v", err)
	}
	postUpdate := time.Now()

	if err := checkInfo(ctx, cs, d1, info, preCommit, postCommit, preUpdate, postUpdate); err != nil {
		t.Fatalf("Check info failed: %+v", err)
	}

	info.Labels = map[string]string{
		"k1": "v1",

		"containerd.io/gc.root": rootTime,
	}
	preUpdate = time.Now()
	if _, err := cs.Update(ctx, info, "labels.k3", "labels.k1"); err != nil {
		t.Fatalf("Update failed: %+v", err)
	}
	postUpdate = time.Now()

	if err := checkInfo(ctx, cs, d1, info, preCommit, postCommit, preUpdate, postUpdate); err != nil {
		t.Fatalf("Check info failed: %+v", err)
	}

}

func checkResume(rf func(context.Context, content.Writer, []byte, int64, int64, digest.Digest) error) func(ctx context.Context, t *testing.T, cs content.Store) {
	return func(ctx context.Context, t *testing.T, cs content.Store) {
		sizes := []int64{500, 5000, 50000}
		truncations := []float64{0.0, 0.1, 0.5, 0.9, 1.0}

		for i, size := range sizes {
			for j, tp := range truncations {
				b, d := createContent(size)
				limit := int64(float64(size) * tp)
				ref := fmt.Sprintf("ref-%d-%d", i, j)

				w, err := content.OpenWriter(ctx, cs, content.WithRef(ref), content.WithDescriptor(ocispec.Descriptor{Size: size, Digest: d}))
				if err != nil {
					t.Fatal(err)
				}

				if _, err := w.Write(b[:limit]); err != nil {
					w.Close()
					t.Fatal(err)
				}

				if err := w.Close(); err != nil {
					t.Fatal(err)
				}

				w, err = content.OpenWriter(ctx, cs, content.WithRef(ref), content.WithDescriptor(ocispec.Descriptor{Size: size, Digest: d}))
				if err != nil {
					t.Fatal(err)
				}

				st, err := w.Status()
				if err != nil {
					w.Close()
					t.Fatal(err)
				}

				if st.Offset != limit {
					w.Close()
					t.Fatalf("Unexpected offset %d, expected %d", st.Offset, limit)
				}

				preCommit := time.Now()
				if err := rf(ctx, w, b, limit, size, d); err != nil {
					t.Fatalf("Resume failed: %+v", err)
				}
				postCommit := time.Now()

				if err := w.Close(); err != nil {
					t.Fatal(err)
				}

				info := content.Info{
					Digest: d,
					Size:   size,
				}

				if err := checkInfo(ctx, cs, d, info, preCommit, postCommit, preCommit, postCommit); err != nil {
					t.Fatalf("Check info failed: %+v", err)
				}
			}
		}
	}
}

func resumeTruncate(ctx context.Context, w content.Writer, b []byte, written, size int64, dgst digest.Digest) error {
	if err := w.Truncate(0); err != nil {
		return fmt.Errorf("truncate failed: %w", err)
	}

	if _, err := io.CopyBuffer(w, bytes.NewReader(b), make([]byte, 1024)); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}
	if err := w.Commit(ctx, size, dgst); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}
	return nil
}

func resumeDiscard(ctx context.Context, w content.Writer, b []byte, written, size int64, dgst digest.Digest) error {
	if _, err := io.CopyBuffer(w, bytes.NewReader(b[written:]), make([]byte, 1024)); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}
	if err := w.Commit(ctx, size, dgst); err != nil {
		return fmt.Errorf("commit failed: %w", err)

	}
	return nil
}

func resumeCopy(ctx context.Context, w content.Writer, b []byte, _, size int64, dgst digest.Digest) error {
	r := struct {
		io.Reader
	}{bytes.NewReader(b)}
	if err := content.Copy(ctx, w, r, size, dgst); err != nil {
		return fmt.Errorf("copy failed: %w", err)
	}
	return nil
}

func resumeCopySeeker(ctx context.Context, w content.Writer, b []byte, _, size int64, dgst digest.Digest) error {
	r := struct {
		io.ReadSeeker
	}{bytes.NewReader(b)}
	if err := content.Copy(ctx, w, r, size, dgst); err != nil {
		return fmt.Errorf("copy failed: %w", err)
	}
	return nil
}

func resumeCopyReaderAt(ctx context.Context, w content.Writer, b []byte, _, size int64, dgst digest.Digest) error {
	type readerAt interface {
		io.Reader
		io.ReaderAt
	}
	r := struct {
		readerAt
	}{bytes.NewReader(b)}
	if err := content.Copy(ctx, w, r, size, dgst); err != nil {
		return fmt.Errorf("copy failed: %w", err)
	}
	return nil
}

// checkSmallBlob tests reading a blob which is smaller than the read size.
func checkSmallBlob(ctx context.Context, t *testing.T, store content.Store) {
	blob := []byte(`foobar`)
	blobSize := int64(len(blob))
	blobDigest := digest.FromBytes(blob)
	// test write
	w, err := store.Writer(ctx, content.WithRef(t.Name()), content.WithDescriptor(ocispec.Descriptor{Size: blobSize, Digest: blobDigest}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(blob); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(ctx, blobSize, blobDigest); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// test read.
	readSize := blobSize + 1
	ra, err := store.ReaderAt(ctx, ocispec.Descriptor{Digest: blobDigest})
	if err != nil {
		t.Fatal(err)
	}
	defer ra.Close()
	r := io.NewSectionReader(ra, 0, readSize)
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := ra.Close(); err != nil {
		t.Fatal(err)
	}
	d := digest.FromBytes(b)
	if blobDigest != d {
		t.Fatalf("expected %s (%q), got %s (%q)", blobDigest, string(blob),
			d, string(b))
	}
}

func checkCrossNSShare(ctx context.Context, t *testing.T, cs content.Store) {
	wrap, ok := ctx.Value(wrapperKey{}).(ContextWrapper)
	if !ok {
		t.Skip("multiple contexts not supported")
	}

	var size int64 = 1000
	b, d := createContent(size)
	ref := fmt.Sprintf("ref-%d", size)
	t1 := time.Now()

	if err := content.WriteBlob(ctx, cs, ref, bytes.NewReader(b), ocispec.Descriptor{Size: size, Digest: d}); err != nil {
		t.Fatal(err)
	}

	ctx2, done, err := wrap(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer done(ctx2)

	w, err := content.OpenWriter(ctx2, cs, content.WithRef(ref), content.WithDescriptor(ocispec.Descriptor{Size: size, Digest: d}))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	t2 := time.Now()

	checkStatus(t, w, content.Status{
		Ref:    ref,
		Offset: size,
		Total:  size,
	}, d, t1, t2, t1, t2)

	if err := w.Commit(ctx2, size, d); err != nil {
		t.Fatal(err)
	}
	t3 := time.Now()

	info := content.Info{
		Digest: d,
		Size:   size,
	}
	if err := checkContent(ctx, cs, d, info, t1, t3, t1, t3); err != nil {
		t.Fatal(err)
	}

	if err := checkContent(ctx2, cs, d, info, t1, t3, t1, t3); err != nil {
		t.Fatal(err)
	}
}

func checkCrossNSAppend(ctx context.Context, t *testing.T, cs content.Store) {
	wrap, ok := ctx.Value(wrapperKey{}).(ContextWrapper)
	if !ok {
		t.Skip("multiple contexts not supported")
	}

	var size int64 = 1000
	b, d := createContent(size)
	ref := fmt.Sprintf("ref-%d", size)
	t1 := time.Now()

	if err := content.WriteBlob(ctx, cs, ref, bytes.NewReader(b), ocispec.Descriptor{Size: size, Digest: d}); err != nil {
		t.Fatal(err)
	}

	ctx2, done, err := wrap(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer done(ctx2)

	extra := []byte("appended bytes")
	size2 := size + int64(len(extra))
	b2 := make([]byte, size2)
	copy(b2[:size], b)
	copy(b2[size:], extra)
	d2 := digest.FromBytes(b2)

	w, err := content.OpenWriter(ctx2, cs, content.WithRef(ref), content.WithDescriptor(ocispec.Descriptor{Size: size, Digest: d}))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	t2 := time.Now()

	checkStatus(t, w, content.Status{
		Ref:    ref,
		Offset: size,
		Total:  size,
	}, d, t1, t2, t1, t2)

	if _, err := w.Write(extra); err != nil {
		t.Fatal(err)
	}

	if err := w.Commit(ctx2, size2, d2); err != nil {
		t.Fatal(err)
	}
	t3 := time.Now()

	info := content.Info{
		Digest: d,
		Size:   size,
	}
	if err := checkContent(ctx, cs, d, info, t1, t3, t1, t3); err != nil {
		t.Fatal(err)
	}

	info2 := content.Info{
		Digest: d2,
		Size:   size2,
	}
	if err := checkContent(ctx2, cs, d2, info2, t1, t3, t1, t3); err != nil {
		t.Fatal(err)
	}

}

func checkCrossNSIsolate(ctx context.Context, t *testing.T, cs content.Store) {
	wrap, ok := ctx.Value(wrapperKey{}).(ContextWrapper)
	if !ok {
		t.Skip("multiple contexts not supported")
	}

	var size int64 = 1000
	b, d := createContent(size)
	ref := fmt.Sprintf("ref-%d", size)
	t1 := time.Now()

	if err := content.WriteBlob(ctx, cs, ref, bytes.NewReader(b), ocispec.Descriptor{Size: size, Digest: d}); err != nil {
		t.Fatal(err)
	}
	t2 := time.Now()

	ctx2, done, err := wrap(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer done(ctx2)

	t3 := time.Now()
	w, err := content.OpenWriter(ctx2, cs, content.WithRef(ref), content.WithDescriptor(ocispec.Descriptor{Size: size, Digest: d}))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	t4 := time.Now()

	checkNewlyCreated(t, w, t1, t2, t3, t4)
}

func checkSharedNSIsolate(ctx context.Context, t *testing.T, cs content.Store) {
	wrap, ok := ctx.Value(wrapperKey{}).(ContextWrapper)
	if !ok {
		t.Skip("multiple contexts not supported")
	}

	ctx1, done1, err := wrap(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer done1(ctx1)

	var size int64 = 1000
	b, d := createContent(size)
	ref := fmt.Sprintf("ref-%d", size)
	t1 := time.Now()

	if err := content.WriteBlob(ctx1, cs, ref, bytes.NewReader(b), ocispec.Descriptor{Size: size, Digest: d}); err != nil {
		t.Fatal(err)
	}

	ctx2, done2, err := wrap(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer done2(ctx2)

	w, err := content.OpenWriter(ctx2, cs, content.WithRef(ref), content.WithDescriptor(ocispec.Descriptor{Size: size, Digest: d}))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	t2 := time.Now()

	checkStatus(t, w, content.Status{
		Ref:    ref,
		Offset: size,
		Total:  size,
	}, d, t1, t2, t1, t2)

	if err := w.Commit(ctx2, size, d); err != nil {
		t.Fatal(err)
	}
	t3 := time.Now()

	info := content.Info{
		Digest: d,
		Size:   size,
	}
	if err := checkContent(ctx1, cs, d, info, t1, t3, t1, t3); err != nil {
		t.Fatal(err)
	}

	if err := checkContent(ctx2, cs, d, info, t1, t3, t1, t3); err != nil {
		t.Fatal(err)
	}
}

func checkStatus(t *testing.T, w content.Writer, expected content.Status, d digest.Digest, preStart, postStart, preUpdate, postUpdate time.Time) {
	t.Helper()
	st, err := w.Status()
	if err != nil {
		t.Fatalf("failed to get status: %v", err)
	}

	wd := w.Digest()
	if wd != d {
		t.Fatalf("unexpected digest %v, expected %v", wd, d)
	}

	if st.Ref != expected.Ref {
		t.Fatalf("unexpected ref %q, expected %q", st.Ref, expected.Ref)
	}

	if st.Offset != expected.Offset {
		t.Fatalf("unexpected offset %d, expected %d", st.Offset, expected.Offset)
	}

	if st.Total != expected.Total {
		t.Fatalf("unexpected total %d, expected %d", st.Total, expected.Total)
	}

	// TODO: Add this test once all implementations guarantee this value is held
	//if st.Expected != expected.Expected {
	//	t.Fatalf("unexpected \"expected digest\" %q, expected %q", st.Expected, expected.Expected)
	//}

	// FIXME: broken on windows: unexpected updated at time 2017-11-14 13:43:22.178013 -0800 PST,
	// expected between 2017-11-14 13:43:22.1790195 -0800 PST m=+1.022137300 and
	// 2017-11-14 13:43:22.1790195 -0800 PST m=+1.022137300
	if runtime.GOOS != "windows" {
		if st.StartedAt.After(postStart) || st.StartedAt.Before(preStart) {
			t.Fatalf("unexpected started at time %s, expected between %s and %s", st.StartedAt, preStart, postStart)
		}

		t.Logf("compare update %v against (%v, %v)", st.UpdatedAt, preUpdate, postUpdate)
		if st.UpdatedAt.After(postUpdate) || st.UpdatedAt.Before(preUpdate) {
			t.Fatalf("unexpected updated at time %s, expected between %s and %s", st.UpdatedAt, preUpdate, postUpdate)
		}
	}
}

func checkNewlyCreated(t *testing.T, w content.Writer, preStart, postStart, preUpdate, postUpdate time.Time) {
	t.Helper()
	st, err := w.Status()
	if err != nil {
		t.Fatalf("failed to get status: %v", err)
	}

	wd := w.Digest()
	if wd != emptyDigest {
		t.Fatalf("unexpected digest %v, expected %v", wd, emptyDigest)
	}

	if st.Offset != 0 {
		t.Fatalf("unexpected offset %v", st.Offset)
	}

	if runtime.GOOS != "windows" {
		if st.StartedAt.After(postUpdate) || st.StartedAt.Before(postStart) {
			t.Fatalf("unexpected started at time %s, expected between %s and %s", st.StartedAt, postStart, postUpdate)
		}
	}
}

func checkInfo(ctx context.Context, cs content.Store, d digest.Digest, expected content.Info, c1, c2, u1, u2 time.Time) error {
	info, err := cs.Info(ctx, d)
	if err != nil {
		return fmt.Errorf("failed to get info: %w", err)
	}

	if info.Digest != d {
		return fmt.Errorf("unexpected info digest %s, expected %s", info.Digest, d)
	}

	if info.Size != expected.Size {
		return fmt.Errorf("unexpected info size %d, expected %d", info.Size, expected.Size)
	}

	if info.CreatedAt.After(c2) || info.CreatedAt.Before(c1) {
		return fmt.Errorf("unexpected created at time %s, expected between %s and %s", info.CreatedAt, c1, c2)
	}
	// FIXME: broken on windows: unexpected updated at time 2017-11-14 13:43:22.178013 -0800 PST,
	// expected between 2017-11-14 13:43:22.1790195 -0800 PST m=+1.022137300 and
	// 2017-11-14 13:43:22.1790195 -0800 PST m=+1.022137300
	if runtime.GOOS != "windows" && (info.UpdatedAt.After(u2) || info.UpdatedAt.Before(u1)) {
		return fmt.Errorf("unexpected updated at time %s, expected between %s and %s", info.UpdatedAt, u1, u2)
	}

	if len(info.Labels) != len(expected.Labels) {
		return fmt.Errorf("mismatched number of labels\ngot:\n%#v\nexpected:\n%#v", info.Labels, expected.Labels)
	}

	for k, v := range expected.Labels {
		actual := info.Labels[k]
		if v != actual {
			return fmt.Errorf("unexpected value for label %q: %q, expected %q", k, actual, v)
		}
	}

	return nil
}
func checkContent(ctx context.Context, cs content.Store, d digest.Digest, expected content.Info, c1, c2, u1, u2 time.Time) error {
	if err := checkInfo(ctx, cs, d, expected, c1, c2, u1, u2); err != nil {
		return err
	}

	b, err := content.ReadBlob(ctx, cs, ocispec.Descriptor{Digest: d})
	if err != nil {
		return fmt.Errorf("failed to read blob: %w", err)
	}

	if int64(len(b)) != expected.Size {
		return fmt.Errorf("wrong blob size %d, expected %d", len(b), expected.Size)
	}

	actual := digest.FromBytes(b)
	if actual != d {
		return fmt.Errorf("wrong digest %s, expected %s", actual, d)
	}

	return nil
}

var contentSeed int64

func createContent(size int64) ([]byte, digest.Digest) {
	// each time we call this, we want to get a different seed, but it should
	// be related to the initialization order and fairly consistent between
	// test runs. An atomic integer works just good enough for this.
	seed := atomic.AddInt64(&contentSeed, 1)

	b, err := io.ReadAll(io.LimitReader(rand.New(rand.NewSource(seed)), size))
	if err != nil {
		panic(err)
	}
	return b, digest.FromBytes(b)
}
