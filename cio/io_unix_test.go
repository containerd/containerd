// +build !windows

package cio

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gotestyourself/gotestyourself/assert"
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
