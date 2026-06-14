//go:build linux

package apparmor

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update golden files")

func TestCleanProfileName(t *testing.T) {
	assert.Equal(t, "unconfined", cleanProfileName(""))
	assert.Equal(t, "unconfined", cleanProfileName("unconfined"))
	assert.Equal(t, "unconfined", cleanProfileName("unconfined\n"))
	assert.Equal(t, "unconfined", cleanProfileName("unconfined (enforce)"))
	assert.Equal(t, "unconfined", cleanProfileName("unconfined (enforce)\n"))
	assert.Equal(t, "docker-default", cleanProfileName("docker-default"))
	assert.Equal(t, "foo", cleanProfileName("foo"))
	assert.Equal(t, "foo", cleanProfileName("foo (enforce)"))
}

func TestGenerateDefault(t *testing.T) {
	tests := []struct {
		name        string
		data        data
		macroExists func(string) bool
	}{
		{
			name: "default",
			data: data{
				Name: "default",
			},
		},
		{
			name: "with-api3",
			data: data{
				Name: "with-api3",
			},
			macroExists: func(name string) bool { return name == "abi/3.0" },
		},
		{
			name: "with-tunables",
			data: data{
				Name: "tunables",
			},
			macroExists: func(name string) bool { return name == "tunables/global" },
		},
		{
			name: "with-abstractions-base",
			data: data{
				Name: "abstractions-base",
			},
			macroExists: func(name string) bool { return name == "abstractions/base" },
		},
		{
			name: "with-daemon-profile",
			data: data{
				Name:          "daemon-profile",
				DaemonProfile: "my-daemon-profile",
			},
		},
		{
			name: "with-custom-imports",
			data: data{
				Name:    "custom-imports",
				Imports: []string{"#include <something/foo>", "#include <something/bar>"},
			},
		},
		{
			name: "with-custom-inner-imports",
			data: data{
				Name:         "custom-inner-imports",
				InnerImports: []string{"#include <something/foo>", "#include <something/bar>"},
			},
		},
		{
			name: "with-rootlesskit",
			data: data{
				Name:        "rootlesskit",
				RootlessKit: "/usr/local/bin/rootlesskit",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// disable all macros by default.
			macroExistsFn := func(string) bool { return false }
			if tc.macroExists != nil {
				macroExistsFn = tc.macroExists
			}
			d, err := loadData(tc.data.Name, macroExistsFn)
			require.NoError(t, err)

			if tc.data.DaemonProfile != "" {
				d.DaemonProfile = tc.data.DaemonProfile
			} else {
				d.DaemonProfile = "unconfined"
			}
			if tc.data.Imports != nil {
				d.Imports = tc.data.Imports
			}
			if tc.data.InnerImports != nil {
				d.InnerImports = tc.data.InnerImports
			}
			d.RootlessKit = tc.data.RootlessKit

			var sb strings.Builder
			err = generate(d, &sb)
			require.NoError(t, err)

			assertGolden(t, sb.String(), tc.name)
		})
	}
}

func assertGolden(t *testing.T, got string, name string) {
	t.Helper()

	goldenFile := filepath.Join("testdata", name+".golden")
	if *update {
		err := os.WriteFile(goldenFile, []byte(got), 0o644)
		if err != nil {
			t.Fatalf("updating golden file %q: %v", goldenFile, err)
		}
	}

	want, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf(`reading golden file: %v

You can run 'go test . -update' to automatically update %s to the new expected value.`, err, goldenFile)
	}

	assert.Equal(t, string(want), got)
}
