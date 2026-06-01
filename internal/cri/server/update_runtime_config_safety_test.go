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

package server

// Characterization / repro-first safety tests for writeCNIConfigFile (todo P1-5).
//
// These tests lock the observable error-handling contract of UpdateRuntimeConfig's
// CNI-config-generation path and MUST pass both before and after the refactor,
// EXCEPT the repro tests which are expected to FAIL on the unfixed code (that FAIL
// is the executable proof the bug is real) and to PASS after the fix.
//
// They drive the real exported entry point criService.UpdateRuntimeConfig with the
// project's own test seam (newTestCRIService + FakeCNIPlugin), using a real
// text/template parsed from a real file — i.e. production-grade usage, no
// artificial internals poking. FakeCNIPlugin's StatusErr/LoadErr are set to the
// errors a not-yet-ready CNI returns in production, which is exactly what steers
// the handler into writeCNIConfigFile (see TestUpdateRuntimeConfig).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	servertesting "github.com/containerd/containerd/v2/internal/cri/testing"
)

// updateRuntimeConfigSafetyEnv wires a criService whose CNI plugin is "not ready"
// (Status + Load both error), so UpdateRuntimeConfig is forced down the
// writeCNIConfigFile path with the given template written to disk.
func updateRuntimeConfigSafetyEnv(t *testing.T, templateBody string) (*criService, *runtime.UpdateRuntimeConfigRequest, string) {
	t.Helper()
	testDir := t.TempDir()
	templateName := filepath.Join(testDir, "template")
	require.NoError(t, os.WriteFile(templateName, []byte(templateBody), 0o666))

	confDir := filepath.Join(testDir, "net.d")
	confName := filepath.Join(confDir, cniConfigFileName)

	c := newTestCRIService()
	c.config.CniConfig = criconfig.CniConfig{
		NetworkPluginConfDir:      confDir,
		NetworkPluginConfTemplate: templateName,
	}
	// Force the "CNI not ready" branch so the handler must (re)generate the
	// config from the template - the production trigger for writeCNIConfigFile.
	c.netPlugin[defaultNetworkPlugin].(*servertesting.FakeCNIPlugin).StatusErr = errors.New("random error")
	c.netPlugin[defaultNetworkPlugin].(*servertesting.FakeCNIPlugin).LoadErr = errors.New("random error")

	req := &runtime.UpdateRuntimeConfigRequest{
		RuntimeConfig: &runtime.RuntimeConfig{
			NetworkConfig: &runtime.NetworkConfig{
				PodCidr: "10.0.0.0/24",
			},
		},
	}
	return c, req, confName
}

// TestUpdateRuntimeConfigSafety_TemplateExecErrorDoesNotCommit is the primary,
// root-independent repro.
//
// Invariant: when template execution fails mid-render, the destination CNI config
// file MUST NOT be created/overwritten with partial (corrupt) content. atomicfile's
// whole point is all-or-nothing: a failed render must abort (Cancel), not commit
// (Close+rename) the half-written temp file.
//
// Pre-fix (unfixed code): the deferred `f.Close()` runs unconditionally and renames
// the partial temp file into place => confName exists with corrupt content => FAIL.
// Post-fix: the render error aborts the temp file => confName is absent => PASS.
func TestUpdateRuntimeConfigSafety_TemplateExecErrorDoesNotCommit(t *testing.T) {
	// Parses fine, but execution errors on the unknown struct field AFTER the
	// static prefix has already been written to the temp file.
	const badTemplate = `{"cniVersion":"1.0.0","subnet":"{{.PodCIDR}}","x":"{{.ThisFieldDoesNotExist}}"}`

	c, req, confName := updateRuntimeConfigSafetyEnv(t, badTemplate)

	_, err := c.UpdateRuntimeConfig(context.Background(), req)
	// The render error must be surfaced to the caller (true pre- and post-fix).
	assert.Error(t, err, "template execution error must propagate to the caller")

	// The discriminating assertion: no partially-written config may be committed.
	_, statErr := os.Stat(confName)
	assert.Truef(t, os.IsNotExist(statErr),
		"a failed template render must not leave a (corrupt) CNI config behind; "+
			"stat(%q) err = %v", confName, statErr)
}

// TestUpdateRuntimeConfigSafety_AtomicNewErrorNoPanic exercises the
// atomicfile.New failure branch.
//
// Invariant: if atomicfile.New fails (e.g. the CNI conf dir is not writable - a
// plausible production IO fault), UpdateRuntimeConfig must return an error
// gracefully and MUST NOT panic. The unfixed code ignores New's error and then
// writes the template into a nil File, which panics; with no gRPC panic-recovery
// interceptor in containerd that crashes the daemon.
//
// Pre-fix: nil-writer panic => test FAILs. Post-fix: clean error return => PASS.
//
// Skipped when running as root, since root bypasses the directory write
// permission this relies on (e.g. inside the golang test container). The
// root-independent proof of the broken error handling lives in
// TestUpdateRuntimeConfigSafety_TemplateExecErrorDoesNotCommit.
func TestUpdateRuntimeConfigSafety_AtomicNewErrorNoPanic(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("read-only directory does not block writes as root; see TemplateExecErrorDoesNotCommit for the root-independent repro")
	}
	const goodTemplate = `{"cniVersion":"1.0.0","subnet":"{{.PodCIDR}}"}`

	c, req, confName := updateRuntimeConfigSafetyEnv(t, goodTemplate)

	// Pre-create the conf dir read-only so MkdirAll is a no-op (dir already
	// exists) but atomicfile.New's os.CreateTemp inside it fails.
	confDir := filepath.Dir(confName)
	require.NoError(t, os.MkdirAll(confDir, 0o555))

	assert.NotPanics(t, func() {
		_, err := c.UpdateRuntimeConfig(context.Background(), req)
		assert.Error(t, err, "a non-writable CNI conf dir must yield an error, not a panic")
	})
}

// TestUpdateRuntimeConfigSafety_SuccessCommitsConfig is a pure characterization
// test of the happy path: it must PASS both before and after the refactor, so the
// fix cannot regress the normal "generate and atomically commit the config"
// behavior.
//
// Invariant: a nil return implies the CNI config file was committed with exactly
// the rendered content.
func TestUpdateRuntimeConfigSafety_SuccessCommitsConfig(t *testing.T) {
	const goodTemplate = `{"cniVersion":"1.0.0","subnet":"{{.PodCIDR}}"}`
	const expected = `{"cniVersion":"1.0.0","subnet":"10.0.0.0/24"}`

	c, req, confName := updateRuntimeConfigSafetyEnv(t, goodTemplate)

	_, err := c.UpdateRuntimeConfig(context.Background(), req)
	require.NoError(t, err)

	got, readErr := os.ReadFile(confName)
	require.NoError(t, readErr, "nil return must mean the config was committed")
	assert.Equal(t, expected, string(got))
}
