//go:build !windows
// +build !windows

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

package cio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestOpenFifos(t *testing.T) {
	scenarios := []*FIFOSet{
		{
			Config: Config{
				Stdin:  "",
				Stdout: filepath.Join("This/does/not/exist", "test-stdout"),
				Stderr: filepath.Join("This/does/not/exist", "test-stderr"),
			},
		},
		{
			Config: Config{
				Stdin:  filepath.Join("This/does/not/exist", "test-stdin"),
				Stdout: "",
				Stderr: filepath.Join("This/does/not/exist", "test-stderr"),
			},
		},
		{
			Config: Config{
				Stdin:  "",
				Stdout: "",
				Stderr: filepath.Join("This/does/not/exist", "test-stderr"),
			},
		},
	}
	for _, scenario := range scenarios {
		_, err := openFifos(context.Background(), scenario)
		assert.Assert(t, err != nil, scenario)
	}
}

// TestOpenFifosWithTerminal tests openFifos should not open stderr if terminal
// is set.
func TestOpenFifosWithTerminal(t *testing.T) {
	var ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	ioFifoDir, err := os.MkdirTemp("", fmt.Sprintf("cio-%s", t.Name()))
	if err != nil {
		t.Fatalf("unexpected error during creating temp dir: %v", err)
	}
	defer os.RemoveAll(ioFifoDir)

	cfg := Config{
		Stdout: filepath.Join(ioFifoDir, "test-stdout"),
		Stderr: filepath.Join(ioFifoDir, "test-stderr"),
	}

	// Without terminal, pipes.Stderr should not be nil
	{
		p, err := openFifos(ctx, NewFIFOSet(cfg, nil))
		if err != nil {
			t.Fatalf("unexpected error during openFifos: %v", err)
		}

		if p.Stderr == nil {
			t.Fatalf("unexpected empty stderr pipe")
		}
	}

	// With terminal, pipes.Stderr should be nil
	{
		cfg.Terminal = true
		p, err := openFifos(ctx, NewFIFOSet(cfg, nil))
		if err != nil {
			t.Fatalf("unexpected error during openFifos: %v", err)
		}

		if p.Stderr != nil {
			t.Fatalf("unexpected stderr pipe")
		}
	}
}
