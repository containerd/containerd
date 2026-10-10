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

package v2

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/pkg/namespaces"
)

// setupFakeShim writes an executable that records its working directory and
// its arguments into recordPath, one per line, and exits without writing
// anything to stdout. Empty stdout unmarshals as a zero-valued
// task.DeleteResponse, which is all (*binary).Delete needs to succeed.
func setupFakeShim(t *testing.T, dir, recordPath string) string {
	t.Helper()

	shimPath := filepath.Join(dir, "containerd-shim-test-v2")
	script := "#!/bin/sh\n{ pwd -P; printf '%s\n' \"$@\"; } > " + recordPath + "\n"
	if err := os.WriteFile(shimPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return shimPath
}

// TestBinaryDeleteWorkDir asserts that the bundle is used as the working
// directory of the shim delete process only while it still exists. The delete
// runs from the shim's connection-close callback, so it races the callers that
// remove the bundle themselves; binding cmd.Dir to an already removed bundle
// made the child's chdir(2) fail before the shim was ever executed.
func TestBinaryDeleteWorkDir(t *testing.T) {
	for _, tc := range []struct {
		name          string
		createBundle  bool
		wantBundleCwd bool
	}{
		{name: "bundle present", createBundle: true, wantBundleCwd: true},
		{name: "bundle already removed", createBundle: false, wantBundleCwd: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			record := filepath.Join(dir, "record")
			shimPath := setupFakeShim(t, dir, record)

			bundle := filepath.Join(dir, "bundle")
			if tc.createBundle {
				if err := os.Mkdir(bundle, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			b := &binary{
				runtime: shimPath,
				bundle:  &Bundle{ID: "test", Path: bundle, Namespace: "testns"},
			}
			ctx := namespaces.WithNamespace(t.Context(), "testns")
			if _, err := b.Delete(ctx); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			out, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) < 2 {
				t.Fatalf("expected a cwd and at least one argument, got %q", out)
			}
			gotCwd, argv := lines[0], lines[1:]

			if used := gotCwd == bundle; used != tc.wantBundleCwd {
				t.Errorf("cwd %q: using bundle = %v, want %v", gotCwd, used, tc.wantBundleCwd)
			}
			// -bundle is passed either way: it is how the shim learns where the
			// bundle is when the working directory cannot tell it.
			if i := slices.Index(argv, "-bundle"); i < 0 || i+1 >= len(argv) || argv[i+1] != bundle {
				t.Errorf("expected -bundle %s in argv, got %v", bundle, argv)
			}
			if argv[len(argv)-1] != "delete" {
				t.Errorf("expected delete action, got %q", argv[len(argv)-1])
			}
		})
	}
}
