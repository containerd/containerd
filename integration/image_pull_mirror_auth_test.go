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

package integration

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/server/images"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

// TestCRIImagePullAllowRequestAuthOnMirrors verifies how the credentials
// supplied on a CRI PullImageRequest are distributed across the registry
// named by the image reference and its configured mirror, based on the
// AllowRequestAuthOnMirrors setting. In both cases the primary registry
// must see the credentials.
func TestCRIImagePullAllowRequestAuthOnMirrors(t *testing.T) {
	// The test writes hosts.toml under a directory named after the primary
	// registry host, which is "127.0.0.1:<port>" — ':' is not valid in a
	// Windows path element.
	if goruntime.GOOS != "linux" {
		t.Skip("Only runs on linux")
	}
	t.Parallel()

	for _, tc := range []struct {
		name            string
		allowOnMirrors  bool
		mirrorSeesCreds bool
	}{
		{name: "False", allowOnMirrors: false, mirrorSeesCreds: false},
		{name: "True", allowOnMirrors: true, mirrorSeesCreds: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Run("LocalPull", func(t *testing.T) {
				t.Parallel()
				testCRIImagePullAllowRequestAuthOnMirrors(t, tc.allowOnMirrors, tc.mirrorSeesCreds, true)
			})
			t.Run("TransferService", func(t *testing.T) {
				t.Parallel()
				testCRIImagePullAllowRequestAuthOnMirrors(t, tc.allowOnMirrors, tc.mirrorSeesCreds, false)
			})
		})
	}
}

func testCRIImagePullAllowRequestAuthOnMirrors(t *testing.T, allowOnMirrors, mirrorSeesCreds, useLocal bool) {
	const (
		testUser   = "user"
		testPasswd = "passwd"
	)
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(testUser+":"+testPasswd))

	tmpDir := t.TempDir()
	cli := buildLocalContainerdClient(t, tmpDir, nil)

	primary, primarySeen := newRecordingRegistry()
	defer primary.Close()
	mirror, mirrorSeen := newRecordingRegistry()
	defer mirror.Close()

	primaryURL, err := url.Parse(primary.URL)
	require.NoError(t, err)

	configPath := filepath.Join(tmpDir, "certs.d")
	hostDir := filepath.Join(configPath, primaryURL.Host)
	require.NoError(t, os.MkdirAll(hostDir, 0700))
	hostsToml := fmt.Sprintf(
		"server = %q\n\n[host.%q]\n  capabilities = [\"pull\", \"resolve\"]\n",
		primary.URL, mirror.URL,
	)
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(hostsToml), 0600))

	registryCfg := criconfig.Registry{
		ConfigPath:                configPath,
		AllowRequestAuthOnMirrors: allowOnMirrors,
	}
	svc, err := initLocalCRIImageService(cli, tmpDir, registryCfg, useLocal)
	require.NoError(t, err)

	// Go through the GRPC surface so the credential callback is built by
	// credentialsForRef, which is the code path we want to exercise.
	gsvc := &images.GRPCCRIImageService{CRIImageService: svc.(*images.CRIImageService)}

	// The transfer service may still be emitting log lines when the subtest
	// returns, so use a plain background context to avoid tying log output to
	// *testing.T (which panics on Write after test completion).
	ctx := namespaces.WithNamespace(context.Background(), k8sNamespace)
	ref := primaryURL.Host + "/test/image:latest"
	_, err = gsvc.PullImage(ctx, &runtime.PullImageRequest{
		Image: &runtime.ImageSpec{Image: ref},
		Auth:  &runtime.AuthConfig{Username: testUser, Password: testPasswd},
	})
	assert.Error(t, err, "both registries always answer 401 so the pull must fail")

	assert.Contains(t, primarySeen(), expectedAuth,
		"primary registry should be offered the request auth")
	assert.NotEmpty(t, mirrorSeen(), "mirror should have been contacted")
	if mirrorSeesCreds {
		assert.Contains(t, mirrorSeen(), expectedAuth,
			"mirror should see the request auth when AllowRequestAuthOnMirrors=true")
	} else {
		assert.NotContains(t, mirrorSeen(), expectedAuth,
			"mirror must not see the request auth when AllowRequestAuthOnMirrors=false")
	}
}

// newRecordingRegistry returns an httptest.Server that always answers 401 and
// a function that returns the Authorization header seen on every request.
func newRecordingRegistry() (*httptest.Server, func() []string) {
	var (
		mu   sync.Mutex
		seen []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}
