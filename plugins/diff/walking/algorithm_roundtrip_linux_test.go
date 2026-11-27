//go:build linux

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

package walking

import (
	"bytes"
	"context"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/diff/apply"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/pkg/reference"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/containerd/v2/plugins/content/local"
)

// memoryLabelStore is a minimal label store so the walking differ can write
// the containerd.io/uncompressed label after committing the blob. The label
// is what carries the DiffID — without a label store, Compare errors out.
type memoryLabelStore struct {
	mu  sync.Mutex
	all map[digest.Digest]map[string]string
}

func newMemoryLabelStore() *memoryLabelStore {
	return &memoryLabelStore{all: map[digest.Digest]map[string]string{}}
}

func (m *memoryLabelStore) Get(d digest.Digest) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.all[d], nil
}

func (m *memoryLabelStore) Set(d digest.Digest, lbls map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.all[d] = lbls
	return nil
}

func (m *memoryLabelStore) Update(d digest.Digest, upd map[string]string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.all[d]
	if !ok {
		cur = map[string]string{}
	}
	for k, v := range upd {
		if v == "" {
			delete(cur, k)
		} else {
			cur[k] = v
		}
	}
	m.all[d] = cur
	return cur, nil
}

// algorithmAwareRegistry is a minimal OCI distribution server that hashes each
// uploaded blob with the algorithm declared in the digest URL query parameter
// rather than hardcoding sha256. Just enough to support a blob push + blob
// pull round-trip; no manifest endpoints.
type algorithmAwareRegistry struct {
	mu     sync.Mutex
	blobs  map[string][]byte // digest string -> raw bytes
	uploads map[string]*bytes.Buffer // upload UUID -> accumulating buffer
	nextID int
}

func newAlgorithmAwareRegistry() *algorithmAwareRegistry {
	return &algorithmAwareRegistry{
		blobs:   map[string][]byte{},
		uploads: map[string]*bytes.Buffer{},
	}
}

var blobPathRe = regexp.MustCompile(`^/v2/[^/]+(?:/[^/]+)*/blobs/([^/]+)$`)
var uploadStartRe = regexp.MustCompile(`^/v2/[^/]+(?:/[^/]+)*/blobs/uploads/?$`)
var uploadCommitRe = regexp.MustCompile(`^/v2/uploads/([^/]+)$`)

func (r *algorithmAwareRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch {
	case req.Method == http.MethodPost && uploadStartRe.MatchString(req.URL.Path):
		// Start an upload session.
		r.mu.Lock()
		r.nextID++
		id := fmt.Sprintf("upload-%d", r.nextID)
		r.uploads[id] = &bytes.Buffer{}
		r.mu.Unlock()
		w.Header().Set("Location", "/v2/uploads/"+id)
		w.WriteHeader(http.StatusAccepted)

	case (req.Method == http.MethodPatch || req.Method == http.MethodPut) && uploadCommitRe.MatchString(req.URL.Path):
		id := uploadCommitRe.FindStringSubmatch(req.URL.Path)[1]
		r.mu.Lock()
		buf, ok := r.uploads[id]
		r.mu.Unlock()
		if !ok {
			http.Error(w, "no such upload", http.StatusNotFound)
			return
		}
		if _, err := io.Copy(buf, req.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if req.Method == http.MethodPatch {
			w.Header().Set("Location", req.URL.Path)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		// PUT finalises. Honour the digest query parameter's algorithm.
		dgst := digest.Digest(req.URL.Query().Get("digest"))
		if err := dgst.Validate(); err != nil {
			http.Error(w, "bad digest: "+err.Error(), http.StatusBadRequest)
			return
		}
		algo := dgst.Algorithm()
		if !algo.Available() {
			http.Error(w, "unsupported algorithm: "+string(algo), http.StatusBadRequest)
			return
		}
		got := algo.FromBytes(buf.Bytes())
		if got != dgst {
			http.Error(w, fmt.Sprintf("digest mismatch: client claimed %s, server hashed %s", dgst, got), http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.blobs[dgst.String()] = buf.Bytes()
		delete(r.uploads, id)
		r.mu.Unlock()
		w.Header().Set("Docker-Content-Digest", dgst.String())
		w.WriteHeader(http.StatusCreated)

	case (req.Method == http.MethodHead || req.Method == http.MethodGet) && blobPathRe.MatchString(req.URL.Path):
		dgst := blobPathRe.FindStringSubmatch(req.URL.Path)[1]
		r.mu.Lock()
		blob, ok := r.blobs[dgst]
		r.mu.Unlock()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(blob)))
		w.Header().Set("Docker-Content-Digest", dgst)
		if req.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(blob)
		} else {
			w.WriteHeader(http.StatusOK)
		}

	default:
		http.NotFound(w, req)
	}
}

// TestAlgorithmRoundtripViaRegistry exercises the full produce → push → pull →
// consume loop: pack a layer with a non-canonical algorithm, push it to a
// mock registry that itself honours the algorithm in the digest URL, pull it
// into a fresh content store, then apply it. The unpacked filesystem must
// match the original, and every digest along the way must remain in the
// requested algorithm namespace.
func TestAlgorithmRoundtripViaRegistry(t *testing.T) {
	testutil.RequiresRoot(t)
	for _, algo := range []digest.Algorithm{digest.SHA384, digest.SHA512} {
		t.Run(algo.String(), func(t *testing.T) {
			if !algo.Available() {
				t.Skipf("algorithm %q not registered in this binary", algo)
			}

			ctx := context.Background()
			root := t.TempDir()

			// --- Pack ---
			srcCS, err := local.NewLabeledStore(filepath.Join(root, "src-content"), newMemoryLabelStore())
			if err != nil {
				t.Fatalf("src content store: %v", err)
			}

			upperDir := filepath.Join(root, "upper")
			if err := os.MkdirAll(filepath.Join(upperDir, "etc"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(upperDir, "etc", "hostname"), []byte("roundtrip\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(upperDir, "README"), []byte("layer body\n"), 0644); err != nil {
				t.Fatal(err)
			}

			lowerDir := filepath.Join(root, "lower")
			if err := os.MkdirAll(lowerDir, 0755); err != nil {
				t.Fatal(err)
			}

			lowerMounts := []mount.Mount{{Type: "bind", Source: lowerDir, Options: []string{"rbind", "ro"}}}
			upperMounts := []mount.Mount{{Type: "bind", Source: upperDir, Options: []string{"rbind", "ro"}}}

			differ := NewWalkingDiff(srcCS)
			desc, err := differ.Compare(ctx, lowerMounts, upperMounts,
				diff.WithDigestAlgorithm(algo),
				diff.WithMediaType(ocispec.MediaTypeImageLayerGzip),
			)
			if err != nil {
				t.Fatalf("Compare: %v", err)
			}
			if got := desc.Digest.Algorithm(); got != algo {
				t.Fatalf("packed blob algorithm: got %q, want %q", got, algo)
			}

			info, err := srcCS.Info(ctx, desc.Digest)
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			diffID, err := digest.Parse(info.Labels[labels.LabelUncompressed])
			if err != nil {
				t.Fatalf("DiffID parse: %v", err)
			}

			// --- Registry ---
			reg := newAlgorithmAwareRegistry()
			srv := httptest.NewServer(reg)
			defer srv.Close()
			u, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}

			resolver := docker.NewResolver(docker.ResolverOptions{
				Hosts: docker.ConfigureDefaultRegistries(
					docker.WithPlainHTTP(func(string) (bool, error) { return true, nil }),
					docker.WithClient(srv.Client()),
				),
			})

			repoRef, err := reference.Parse(u.Host + "/test-repo:latest")
			if err != nil {
				t.Fatalf("ref parse: %v", err)
			}

			// --- Push ---
			pusher, err := resolver.Pusher(ctx, repoRef.String())
			if err != nil {
				t.Fatalf("Pusher: %v", err)
			}
			cw, err := pusher.Push(ctx, desc)
			if err != nil {
				t.Fatalf("Push open: %v", err)
			}
			ra, err := srcCS.ReaderAt(ctx, desc)
			if err != nil {
				t.Fatalf("ReaderAt: %v", err)
			}
			if _, err := io.Copy(cw, content.NewReader(ra)); err != nil {
				ra.Close()
				t.Fatalf("Push body: %v", err)
			}
			ra.Close()
			if err := cw.Commit(ctx, desc.Size, desc.Digest); err != nil {
				t.Fatalf("Push commit: %v", err)
			}

			// Registry must now hold the blob under the chosen algorithm.
			reg.mu.Lock()
			_, stored := reg.blobs[desc.Digest.String()]
			reg.mu.Unlock()
			if !stored {
				t.Fatalf("registry did not receive blob %s", desc.Digest)
			}

			// --- Pull ---
			dstCS, err := local.NewLabeledStore(filepath.Join(root, "dst-content"), newMemoryLabelStore())
			if err != nil {
				t.Fatalf("dst content store: %v", err)
			}
			fetcher, err := resolver.Fetcher(ctx, repoRef.String())
			if err != nil {
				t.Fatalf("Fetcher: %v", err)
			}
			rc, err := fetcher.Fetch(ctx, desc)
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if err := content.WriteBlob(ctx, dstCS, "pulled-"+algo.String(), rc, desc); err != nil {
				rc.Close()
				t.Fatalf("WriteBlob: %v", err)
			}
			rc.Close()

			// The pulled blob must agree on digest (which means agree on algo).
			pulledRA, err := dstCS.ReaderAt(ctx, desc)
			if err != nil {
				t.Fatalf("pulled ReaderAt: %v", err)
			}
			pulledRA.Close()

			// --- Apply ---
			unpackDir := filepath.Join(root, "unpack")
			if err := os.MkdirAll(unpackDir, 0755); err != nil {
				t.Fatal(err)
			}
			unpackMounts := []mount.Mount{{Type: "bind", Source: unpackDir, Options: []string{"rbind", "rw"}}}

			applier := apply.NewFileSystemApplier(dstCS)
			applied, err := applier.Apply(ctx, desc, unpackMounts,
				diff.WithApplyDigestAlgorithm(algo),
			)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if applied.Digest != diffID {
				t.Fatalf("post-pull DiffID mismatch: got %s, want %s", applied.Digest, diffID)
			}

			// Files round-tripped intact?
			gotHost, err := os.ReadFile(filepath.Join(unpackDir, "etc", "hostname"))
			if err != nil || string(gotHost) != "roundtrip\n" {
				t.Fatalf("etc/hostname: got %q err=%v", gotHost, err)
			}
			gotReadme, err := os.ReadFile(filepath.Join(unpackDir, "README"))
			if err != nil || string(gotReadme) != "layer body\n" {
				t.Fatalf("README: got %q err=%v", gotReadme, err)
			}
		})
	}
}
