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
	"io/ioutil"
	"math/rand"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/testutil"
	"github.com/gotestyourself/gotestyourself/assert"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

// ContentSuite runs a test suite on the content store given a factory function.
func ContentSuite(t *testing.T, name string, storeFn func(ctx context.Context, root string) (context.Context, content.Store, func() error, error)) {
	t.Run("Writer", makeTest(t, name, storeFn, checkContentStoreWriter))
	t.Run("UpdateStatus", makeTest(t, name, storeFn, checkUpdateStatus))
	t.Run("Resume", makeTest(t, name, storeFn, checkResumeWriter))
	t.Run("ResumeTruncate", makeTest(t, name, storeFn, checkResume(resumeTruncate)))
	t.Run("ResumeDiscard", makeTest(t, name, storeFn, checkResume(resumeDiscard)))
	t.Run("ResumeCopy", makeTest(t, name, storeFn, checkResume(resumeCopy)))
	t.Run("ResumeCopySeeker", makeTest(t, name, storeFn, checkResume(resumeCopySeeker)))
	t.Run("ResumeCopyReaderAt", makeTest(t, name, storeFn, checkResume(resumeCopyReaderAt)))
	t.Run("Labels", makeTest(t, name, storeFn, checkLabels))

	t.Run("CrossNamespaceAppend", makeTest(t, name, storeFn, checkCrossNSAppend))
	t.Run("CrossNamespaceShare", makeTest(t, name, storeFn, checkCrossNSShare))
}

// ContextWrapper is used to decorate new context used inside the test
// before using the context on the content store.
// This can be used to support leasing and multiple namespaces tests.
type ContextWrapper func(ctx context.Context) (context.Context, func() error, error)

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

		tmpDir, err := ioutil.TempDir("", "content-suite-"+name+"-")
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
			var done func() error
			ctx, done, err = w(ctx)
			if err != nil {
				t.Fatalf("Error wrapping context: %+v", err)
			}
			defer func() {
				if err := done(); err != nil && !t.Failed() {
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
	w1, err := cs.Writer(ctx, "c1", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer w1.Close()

	c2, d2 := createContent(256)
	w2, err := cs.Writer(ctx, "c2", int64(len(c2)), "")
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	c3, d3 := createContent(256)
	w3, err := cs.Writer(ctx, "c3", 0, d3)
	if err != nil {
		t.Fatal(err)
	}
	defer w3.Close()

	c4, d4 := createContent(256)
	w4, err := cs.Writer(ctx, "c4", int64(len(c4)), d4)
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
	w1, err := cs.Writer(ctx, ref, 256, dgst)
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
	assert.NilError(t, w1.Close(), "close first writer")

	w2, err := cs.Writer(ctx, ref, 256, dgst)
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

	assert.NilError(t, w2.Close(), "close second writer")
	info := content.Info{
		Digest: dgst,
		Size:   256,
	}

	if err := checkInfo(ctx, cs, dgst, info, preCommit, postCommit, preCommit, postCommit); err != nil {
		t.Fatalf("Check info failed: %+v", err)
	}
}

func checkUpdateStatus(ctx context.Context, t *testing.T, cs content.Store) {
	c1, d1 := createContent(256)

	preStart := time.Now()
	w1, err := cs.Writer(ctx, "c1", 256, d1)
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

	w1, err := cs.Writer(ctx, "c1", 256, d1)
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

				w, err := cs.Writer(ctx, ref, size, d)
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

				w, err = cs.Writer(ctx, ref, size, d)
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
		return errors.Wrap(err, "truncate failed")
	}

	if _, err := io.CopyBuffer(w, bytes.NewReader(b), make([]byte, 1024)); err != nil {
		return errors.Wrap(err, "write failed")
	}

	return errors.Wrap(w.Commit(ctx, size, dgst), "commit failed")
}

func resumeDiscard(ctx context.Context, w content.Writer, b []byte, written, size int64, dgst digest.Digest) error {
	if _, err := io.CopyBuffer(w, bytes.NewReader(b[written:]), make([]byte, 1024)); err != nil {
		return errors.Wrap(err, "write failed")
	}
	return errors.Wrap(w.Commit(ctx, size, dgst), "commit failed")
}

func resumeCopy(ctx context.Context, w content.Writer, b []byte, _, size int64, dgst digest.Digest) error {
	r := struct {
		io.Reader
	}{bytes.NewReader(b)}
	return errors.Wrap(content.Copy(ctx, w, r, size, dgst), "copy failed")
}

func resumeCopySeeker(ctx context.Context, w content.Writer, b []byte, _, size int64, dgst digest.Digest) error {
	r := struct {
		io.ReadSeeker
	}{bytes.NewReader(b)}
	return errors.Wrap(content.Copy(ctx, w, r, size, dgst), "copy failed")
}

func resumeCopyReaderAt(ctx context.Context, w content.Writer, b []byte, _, size int64, dgst digest.Digest) error {
	type readerAt interface {
		io.Reader
		io.ReaderAt
	}
	r := struct {
		readerAt
	}{bytes.NewReader(b)}
	return errors.Wrap(content.Copy(ctx, w, r, size, dgst), "copy failed")
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

	if err := content.WriteBlob(ctx, cs, ref, bytes.NewReader(b), size, d); err != nil {
		t.Fatal(err)
	}

	ctx2, done, err := wrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()

	w, err := cs.Writer(ctx2, ref, size, d)
	if err != nil {
		t.Fatal(err)
	}
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

	if err := content.WriteBlob(ctx, cs, ref, bytes.NewReader(b), size, d); err != nil {
		t.Fatal(err)
	}

	ctx2, done, err := wrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()

	extra := []byte("appended bytes")
	size2 := size + int64(len(extra))
	b2 := make([]byte, size2)
	copy(b2[:size], b)
	copy(b2[size:], extra)
	d2 := digest.FromBytes(b2)

	w, err := cs.Writer(ctx2, ref, size, d)
	if err != nil {
		t.Fatal(err)
	}
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

func checkInfo(ctx context.Context, cs content.Store, d digest.Digest, expected content.Info, c1, c2, u1, u2 time.Time) error {
	info, err := cs.Info(ctx, d)
	if err != nil {
		return errors.Wrap(err, "failed to get info")
	}

	if info.Digest != d {
		return errors.Errorf("unexpected info digest %s, expected %s", info.Digest, d)
	}

	if info.Size != expected.Size {
		return errors.Errorf("unexpected info size %d, expected %d", info.Size, expected.Size)
	}

	if info.CreatedAt.After(c2) || info.CreatedAt.Before(c1) {
		return errors.Errorf("unexpected created at time %s, expected between %s and %s", info.CreatedAt, c1, c2)
	}
	// FIXME: broken on windows: unexpected updated at time 2017-11-14 13:43:22.178013 -0800 PST,
	// expected between 2017-11-14 13:43:22.1790195 -0800 PST m=+1.022137300 and
	// 2017-11-14 13:43:22.1790195 -0800 PST m=+1.022137300
	if runtime.GOOS != "windows" && (info.UpdatedAt.After(u2) || info.UpdatedAt.Before(u1)) {
		return errors.Errorf("unexpected updated at time %s, expected between %s and %s", info.UpdatedAt, u1, u2)
	}

	if len(info.Labels) != len(expected.Labels) {
		return errors.Errorf("mismatched number of labels\ngot:\n%#v\nexpected:\n%#v", info.Labels, expected.Labels)
	}

	for k, v := range expected.Labels {
		actual := info.Labels[k]
		if v != actual {
			return errors.Errorf("unexpected value for label %q: %q, expected %q", k, actual, v)
		}
	}

	return nil
}
func checkContent(ctx context.Context, cs content.Store, d digest.Digest, expected content.Info, c1, c2, u1, u2 time.Time) error {
	if err := checkInfo(ctx, cs, d, expected, c1, c2, u1, u2); err != nil {
		return err
	}

	b, err := content.ReadBlob(ctx, cs, d)
	if err != nil {
		return errors.Wrap(err, "failed to read blob")
	}

	if int64(len(b)) != expected.Size {
		return errors.Errorf("wrong blob size %d, expected %d", len(b), expected.Size)
	}

	actual := digest.FromBytes(b)
	if actual != d {
		return errors.Errorf("wrong digest %s, expected %s", actual, d)
	}

	return nil
}

var contentSeed int64

func createContent(size int64) ([]byte, digest.Digest) {
	// each time we call this, we want to get a different seed, but it should
	// be related to the intitialization order and fairly consistent between
	// test runs. An atomic integer works just good enough for this.
	seed := atomic.AddInt64(&contentSeed, 1)

	b, err := ioutil.ReadAll(io.LimitReader(rand.New(rand.NewSource(seed)), size))
	if err != nil {
		panic(err)
	}
	return b, digest.FromBytes(b)
}
