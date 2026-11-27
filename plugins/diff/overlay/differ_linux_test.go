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

package overlay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/containerd/continuity/fs"
	"github.com/containerd/errdefs"
	"github.com/moby/sys/mountinfo"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/core/mount"
)

// The lower mount may use rbind and include submounts that are absent from
// the overlay lowerdir; in that case only the walking differ sees the correct
// lower tree. Exact mountpoint boundaries matter here ("/lower2" is a peer).
func TestHasNestedLowerMount(t *testing.T) {
	root := "/tmp/containerd-mount"
	for _, tc := range []struct {
		name      string
		paths     []string
		nested    bool
		foundRoot bool
	}{
		{name: "root only", paths: []string{root}, foundRoot: true},
		{name: "nested mount", paths: []string{root, root + "/nested"}, nested: true, foundRoot: true},
		{name: "deep nested mount", paths: []string{root, root + "/nested/deep"}, nested: true, foundRoot: true},
		{name: "sibling path", paths: []string{root, root + "2/nested"}, foundRoot: true},
		{name: "unrelated mount", paths: []string{"/tmp/another"}, foundRoot: false},
		{name: "submount without root", paths: []string{root + "/nested"}, nested: true, foundRoot: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := make([]*mountinfo.Info, 0, len(tc.paths))
			for _, path := range tc.paths {
				entries = append(entries, &mountinfo.Info{Mountpoint: path})
			}
			nested, foundRoot := hasNestedLowerMount(root, entries)
			if nested != tc.nested || foundRoot != tc.foundRoot {
				t.Fatalf("hasNestedLowerMount(%q, %v) = (%v, %v), want (%v, %v)",
					root, tc.paths, nested, foundRoot, tc.nested, tc.foundRoot)
			}
		})
	}
}

func TestGetUpperDir(t *testing.T) {
	tests := []struct {
		name     string
		mounts   []mount.Mount
		expected string
	}{
		{
			name:     "empty mounts",
			mounts:   []mount.Mount{},
			expected: "",
		},
		{
			name: "non-overlay mount",
			mounts: []mount.Mount{
				{
					Type:    "bind",
					Source:  "/some/path",
					Options: []string{"rw"},
				},
			},
			expected: "",
		},
		{
			name: "overlay mount with upperdir",
			mounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"lowerdir=/lower",
						"upperdir=/upper/diff",
						"workdir=/upper/work",
					},
				},
			},
			expected: "/upper/diff",
		},
		{
			name: "overlay mount without upperdir (read-only)",
			mounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"lowerdir=/lower1:/lower2",
					},
				},
			},
			expected: "",
		},
		{
			name: "multiple mounts with overlay last",
			mounts: []mount.Mount{
				{
					Type:   "bind",
					Source: "/bind/source",
				},
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"lowerdir=/lower",
						"upperdir=/data/upper",
						"workdir=/data/work",
					},
				},
			},
			expected: "",
		},
		{
			name: "multiple mounts with non-overlay last",
			mounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"upperdir=/upper",
					},
				},
				{
					Type:   "bind",
					Source: "/bind/source",
				},
			},
			expected: "",
		},
		{
			name: "overlay with complex real-world options",
			mounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=off",
						"lowerdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/1/fs",
						"upperdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/2/fs",
						"workdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/2/work",
					},
				},
			},
			expected: "/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/2/fs",
		},

		{
			name: "overlay targeted at subdirectory",
			mounts: []mount.Mount{{
				Type: "overlay",
				Target: "/subdir",
				Options: []string{"upperdir=/upper"},
			}},
			expected: "",
		},
		{
			name: "overlay targeted at root",
			mounts: []mount.Mount{{
				Type: "overlay",
				Target: "/",
				Options: []string{"upperdir=/upper"},
			}},
			expected: "/upper",
		},
		{
			name: "comma-joined overlay options",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{"upperdir=/upper,workdir=/work"},
			}},
			expected: "",
		},
		{
			name: "kernel escaped overlay path",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{`upperdir=/hello\\040world`},
			}},
			expected: "",
		},
		{
			name: "metacopy enabled",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{"upperdir=/upper", "metacopy=on"},
			}},
			expected: "",
		},
		{
			name: "redirect directory enabled",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{"upperdir=/upper", "redirect_dir=on"},
			}},
			expected: "",
		},
		{
			name: "indexed overlay hardlinks enabled",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{"lowerdir=/lower", "upperdir=/upper", "index=on"},
			}},
			expected: "",
		},
		{
			name: "indexed overlay hardlinks disabled",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{"lowerdir=/lower", "upperdir=/upper", "index=off"},
			}},
			expected: "/upper",
		},
		{
			name: "NFS export requires overlay index",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{"lowerdir=/lower", "upperdir=/upper", "nfs_export=on"},
			}},
			expected: "",
		},
		{
			name: "redirect directory disabled",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{"upperdir=/upper", "redirect_dir=off"},
			}},
			expected: "/upper",
		},
		{
			name: "duplicate upperdir options",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{"upperdir=/a", "upperdir=/b"},
			}},
			expected: "",
		},
		{
			name: "overlay with userxattr whiteouts",
			mounts: []mount.Mount{{
				Type:    "overlay",
				Options: []string{"lowerdir=/lower", "upperdir=/upper", "userxattr"},
			}},
			expected: "",
		},
		{
			name: "idmapped upper mount",
			mounts: []mount.Mount{{
				Type:    "overlay",
				Options: []string{"lowerdir=/lower", "upperdir=/upper", "uidmap=0:100000:65536"},
			}},
			expected: "",
		},
		{
			name: "gidmapped upper mount",
			mounts: []mount.Mount{{
				Type:    "overlay",
				Options: []string{"lowerdir=/lower", "upperdir=/upper", "gidmap=0:100000:65536"},
			}},
			expected: "",
		},

	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getUpperDir(tt.mounts)
			if result != tt.expected {
				t.Errorf("getUpperDir() = %q, want %q", result, tt.expected)
			}
		})
	}
}


// Overlay-typed mount entries can still perform bind or remount operations.
// Their Source, rather than upperdir/lowerdir, controls the mounted view.
func TestGetUpperDirRejectsNonOverlayMountOperations(t *testing.T) {
	for _, operation := range []string{"bind", "rbind", "remount"} {
		t.Run(operation, func(t *testing.T) {
			mounts := []mount.Mount{{
				Type:    "overlay",
				Source:  "/source",
				Options: []string{"lowerdir=/lower", "upperdir=/upper", operation},
			}}
			if got := getUpperDir(mounts); got != "" {
				t.Fatalf("getUpperDir() with %q = %q, want fallback", operation, got)
			}
		})
	}
}

func TestOverlayLowerMatchesRejectsNonOverlayMountOperations(t *testing.T) {
	upper := []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/lower", "upperdir=/upper"}}}
	lower := []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/lower"}}}
	for _, operation := range []string{"bind", "rbind", "remount"} {
		t.Run("upper_"+operation, func(t *testing.T) {
			invalidUpper := []mount.Mount{{
				Type:    "overlay",
				Source:  "/source",
				Options: []string{"lowerdir=/lower", "upperdir=/upper", operation},
			}}
			if overlayLowerMatches(lower, invalidUpper) {
				t.Fatalf("accepted overlay-typed upper mount with %q", operation)
			}
		})
		t.Run("lower_"+operation, func(t *testing.T) {
			invalidLower := []mount.Mount{{
				Type:    "overlay",
				Source:  "/source",
				Options: []string{"lowerdir=/lower", operation},
			}}
			if overlayLowerMatches(invalidLower, upper) {
				t.Fatalf("accepted overlay-typed lower mount with %q", operation)
			}
		})
	}

	// An ordinary bind lower mount is still supported when it matches the
	// overlay's single lowerdir; only overlay-typed mounts are restricted.
	for _, option := range []string{"bind", "rbind"} {
		t.Run("valid_bind_lower_"+option, func(t *testing.T) {
			bindLower := []mount.Mount{{Type: "bind", Source: "/lower", Options: []string{option}}}
			if !overlayLowerMatches(bindLower, upper) {
				t.Fatalf("rejected ordinary bind lower mount with %q", option)
			}
		})
	}
}

// TestOverlayLowerMatches verifies that an overlay upperdir is never used as
// a diff against a different base snapshot.
// Module defaults only govern features without an explicit mount override.
// These cases avoid depending on the CI runner's overlay module parameters.
func TestOverlayFastPathFeatureMountOverrides(t *testing.T) {
	tests := []struct {
		name         string
		options      []string
		moduleValues map[string]string
		want         bool
		reads        []string
	}{
		{
			name:         "defaults disabled",
			moduleValues: map[string]string{"metacopy": "N", "redirect_dir": "N", "index": "N"},
			want:         true,
			reads:        []string{"metacopy", "redirect_dir", "index"},
		},
		{
			name:         "index off overrides enabled default",
			options:      []string{"index=off"},
			moduleValues: map[string]string{"metacopy": "N", "redirect_dir": "N", "index": "Y"},
			want:         true,
			reads:        []string{"metacopy", "redirect_dir"},
		},
		{
			name:         "metacopy off overrides enabled default",
			options:      []string{"metacopy=off"},
			moduleValues: map[string]string{"metacopy": "Y", "redirect_dir": "N", "index": "N"},
			want:         true,
			reads:        []string{"redirect_dir", "index"},
		},
		{
			name:         "redirect off overrides enabled default",
			options:      []string{"redirect_dir=off"},
			moduleValues: map[string]string{"metacopy": "N", "redirect_dir": "Y", "index": "N"},
			want:         true,
			reads:        []string{"metacopy", "index"},
		},
		{
			name:    "all explicitly disabled even when parameters unavailable",
			options: []string{"metacopy=off", "redirect_dir=off", "index=off"},
			want:    true,
		},
		{
			name:         "enabled module default blocks fast path",
			moduleValues: map[string]string{"metacopy": "Y", "redirect_dir": "N", "index": "N"},
			reads:        []string{"metacopy"},
		},
		{
			name:  "missing module parameter blocks fast path",
			reads: []string{"metacopy"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotReads []string
			reader := func(path string) ([]byte, error) {
				feature := filepath.Base(path)
				gotReads = append(gotReads, feature)
				value, ok := tc.moduleValues[feature]
				if !ok {
					return nil, os.ErrNotExist
				}
				return []byte(value + "\n"), nil
			}
			got := overlayFastPathFeaturesDisabledWithReader(tc.options, reader)
			if got != tc.want {
				t.Fatalf("overlayFastPathFeaturesDisabledWithReader(%v) = %v, want %v", tc.options, got, tc.want)
			}
			if !slices.Equal(gotReads, tc.reads) {
				t.Fatalf("read module parameters %v, want %v", gotReads, tc.reads)
			}
		})
	}
}

func TestOverlayLowerMatches(t *testing.T) {
	tests := []struct {
		name  string
		lower []mount.Mount
		upper []mount.Mount
		want  bool
	}{
		{
			name:  "matching bind lower",
			lower: []mount.Mount{{Type: "bind", Source: "/layers/one"}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/two"}}},
			want:  true,
		},
		{
			name:  "unrelated bind lower",
			lower: []mount.Mount{{Type: "bind", Source: "/different"}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/two"}}},
		},
		{
			name:  "matching readonly overlay",
			lower: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one:/layers/two"}}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one:/layers/two", "upperdir=/layers/three"}}},
			want:  true,
		},
		{
			name:  "different overlay lower stack",
			lower: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/other"}}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/three"}}},
		},
		{
			name:  "mutable overlay lower",
			lower: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/other"}}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/three"}}},
		},
		{
			name:  "empty base",
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/two"}}},
		},
		{
			name:  "subdirectory bind",
			lower: []mount.Mount{{Type: "bind", Source: "/layers/one", Target: "/subdir"}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/two"}}},
		},
		{
			name:  "multi-layer upper versus bind lower",
			lower: []mount.Mount{{Type: "bind", Source: "/layers/one"}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one:/layers/two", "upperdir=/layers/three"}}},
		},
		{
			name:  "idmapped bind lower",
			lower: []mount.Mount{{Type: "bind", Source: "/layers/one", Options: []string{"uidmap=0:100000:65536"}}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/two"}}},
		},
		{
			name:  "gidmapped overlay lower",
			lower: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "gidmap=0:100000:65536"}}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/two"}}},
		},

	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := overlayLowerMatches(tt.lower, tt.upper); got != tt.want {
				t.Fatalf("overlayLowerMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}


// Regular-file whiteouts with an overlay.whiteout xattr are not converted
// by continuity's DiffDirChanges. Use the walking differ rather than archive
// them as ordinary files.
func TestOverlayXattrWhiteoutUsesWalkingFallback(t *testing.T) {
	lower, upper := t.TempDir(), t.TempDir()
	path := filepath.Join(upper, "gone")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lsetxattr(path, "user.overlay.whiteout", nil, 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			t.Skipf("overlay whiteout xattrs not supported: %v", err)
		}
		t.Fatal(err)
	}

	changes := 0
	err := fs.DiffDirChanges(context.Background(), lower, upper, fs.DiffSourceOverlayFS,
		withOverlayWhiteoutFallback(upper, func(fs.ChangeKind, string, os.FileInfo, error) error {
			changes++
			return nil
		}))
	if !errdefs.IsNotImplemented(err) {
		t.Fatalf("expected walking fallback for an xattr whiteout, got %v", err)
	}
	if changes != 0 {
		t.Fatalf("received %d changes before rejecting the xattr whiteout, want 0", changes)
	}
}

// The continuity differ reads both opaque xattr namespaces, but only the
// overlay mount knows which namespace is authoritative. Fall back to the
// walking differ rather than exporting a possibly spurious deletion.
func TestOverlayOpaqueMarkerUsesWalkingFallback(t *testing.T) {
	called := false
	wrapped := withOverlayWhiteoutFallback(t.TempDir(), func(fs.ChangeKind, string, os.FileInfo, error) error {
		called = true
		return nil
	})
	if err := wrapped(fs.ChangeKindDelete, "/dir/.wh..opq", nil, nil); !errdefs.IsNotImplemented(err) {
		t.Fatalf("opaque directory marker should trigger walking fallback: %v", err)
	}
	if called {
		t.Fatal("opaque marker unexpectedly reached change writer")
	}
	if err := wrapped(fs.ChangeKindDelete, "/dir/gone", nil, nil); err != nil {
		t.Fatalf("regular deletion unexpectedly rejected: %v", err)
	}
	if !called {
		t.Fatal("regular deletion was not forwarded")
	}
}

// Opacity on the upperdir root is invisible to DiffDirChanges because it
// skips the root. An opaque root can mask inherited lower-layer entries, so
// a raw upperdir diff must defer to the mounted-view walking differ.
func TestOverlayRootOpacityRequiresWalkingFallback(t *testing.T) {
	upper := t.TempDir()
	if err := unix.Lsetxattr(upper, "user.overlay.opaque", []byte("y"), 0); err != nil {
		t.Skipf("filesystem does not support setting overlay xattr: %v", err)
	}
	var result bytes.Buffer
	err := writeDiff(context.Background(), &result, nil, upper, nil)
	if !errdefs.IsNotImplemented(err) {
		t.Fatalf("expected root opacity to require walking fallback, got %v", err)
	}
	if result.Len() != 0 {
		t.Fatalf("wrote %d bytes before detecting opaque upperdir root", result.Len())
	}
}

// A read-only lower overlay with userxattr cannot be matched to the raw
// upperdir's lower stack merely by matching paths: xattr namespaces matter.
func TestOverlayLowerMatchesRejectsUserXattr(t *testing.T) {
	upper := []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/lower", "upperdir=/upper"}}}
	for _, lower := range []mount.Mount{
		{Type: "bind", Source: "/lower", Options: []string{"ro", "userxattr"}},
		{Type: "overlay", Options: []string{"lowerdir=/lower", "userxattr"}},
		{Type: "overlay", Options: []string{"lowerdir=/lower", "userxattr=on"}},
	} {
		if overlayLowerMatches([]mount.Mount{lower}, upper) {
			t.Fatalf("userxattr lower mount unexpectedly accepted: %+v", lower)
		}
	}
}

// errorCloser verifies that an error from compressor Close is not discarded.
type errorCloser struct {
	io.Writer
	closeErr error
	closed   bool
}

func (c *errorCloser) Close() error {
	c.closed = true
	return c.closeErr
}

func TestFinalizeCompressedDiff(t *testing.T) {
	writeErr := errors.New("write failed")
	closeErr := errors.New("close failed")
	for _, tt := range []struct {
		name     string
		writeErr error
		closeErr error
	}{
		{name: "success"},
		{name: "write error", writeErr: writeErr},
		{name: "close error", closeErr: closeErr},
		{name: "both errors", writeErr: writeErr, closeErr: closeErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stream := &errorCloser{Writer: io.Discard, closeErr: tt.closeErr}
			err := finalizeCompressedDiff(stream, tt.writeErr)
			if !stream.closed {
				t.Fatal("compressor was not closed")
			}
			if (tt.writeErr == nil && tt.closeErr == nil && err != nil) ||
				(tt.writeErr != nil && !errors.Is(err, tt.writeErr)) ||
				(tt.closeErr != nil && !errors.Is(err, tt.closeErr)) {
				t.Fatalf("finalizeCompressedDiff() = %v, expected write=%v close=%v", err, tt.writeErr, tt.closeErr)
			}
		})
	}
}

// BenchmarkGetUpperDir measures the cost of parsing overlay mount options
// to extract the upperdir. This is called on every Compare invocation.
func BenchmarkGetUpperDir(b *testing.B) {
	mounts := []mount.Mount{
		{
			Type:   "overlay",
			Source: "overlay",
			Options: []string{
				"index=off",
				"lowerdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/1/fs",
				"upperdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/2/fs",
				"workdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/2/work",
			},
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = getUpperDir(mounts)
	}
}

