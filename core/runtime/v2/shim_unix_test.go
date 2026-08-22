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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/fifo"

	"github.com/containerd/containerd/v2/pkg/namespaces"
)

func TestCheckCopyShimLogError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	if err := checkCopyShimLogError(ctx, fifo.ErrReadClosed); err != fifo.ErrReadClosed {
		t.Fatalf("should return the actual error before context is done, but %v", err)
	}
	if err := checkCopyShimLogError(ctx, nil); err != nil {
		t.Fatalf("should return the actual error before context is done, but %v", err)
	}

	cancel()

	if err := checkCopyShimLogError(ctx, fifo.ErrReadClosed); err != nil {
		t.Fatalf("should return nil when error is ErrReadClosed after context is done, but %v", err)
	}
	if err := checkCopyShimLogError(ctx, nil); err != nil {
		t.Fatalf("should return the actual error after context is done, but %v", err)
	}
	if err := checkCopyShimLogError(ctx, os.ErrClosed); err != nil {
		t.Fatalf("should return the actual error after context is done, but %v", err)
	}
	if err := checkCopyShimLogError(ctx, fifo.ErrRdFrmWRONLY); err != fifo.ErrRdFrmWRONLY {
		t.Fatalf("should return the actual error after context is done, but %v", err)
	}
}

func TestBinaryDeleteWorkDirFallback(t *testing.T) {
	testCases := []struct {
		name              string
		createBundle      bool
		wantBundleWorkDir bool
	}{
		{name: "bundle exists", createBundle: true, wantBundleWorkDir: true},
		{name: "bundle removed", wantBundleWorkDir: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			runtimePath := filepath.Join(tempDir, "containerd-shim-test-v2")
			outputPath := runtimePath + ".output"
			if err := os.WriteFile(runtimePath, []byte("#!/bin/sh\npwd > \"$0.output\"\nprintf '%s\\n' \"$@\" >> \"$0.output\"\n"), 0700); err != nil {
				t.Fatal(err)
			}

			bundlePath := filepath.Join(tempDir, "bundle")
			if tc.createBundle {
				if err := os.Mkdir(bundlePath, 0700); err != nil {
					t.Fatal(err)
				}
			}
			b := &binary{
				runtime: runtimePath,
				bundle: &Bundle{
					ID:        "test",
					Path:      bundlePath,
					Namespace: "test",
				},
			}
			ctx := namespaces.WithNamespace(context.Background(), "test")
			if _, err := b.Delete(ctx); err != nil {
				t.Fatalf("delete failed: %v", err)
			}

			data, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			output := string(data)
			usedBundleWorkDir := strings.HasPrefix(output, bundlePath+"\n")
			if usedBundleWorkDir != tc.wantBundleWorkDir {
				t.Fatalf("unexpected delete shim cwd: %q", output)
			}
			if !strings.Contains(output, "\n-bundle\n"+bundlePath+"\n") {
				t.Fatalf("delete shim did not receive bundle path: %q", output)
			}
		})
	}
}
