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

package client

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	exec "golang.org/x/sys/execabs"

	. "github.com/containerd/containerd"
	"github.com/containerd/containerd/defaults"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	imagelist "github.com/containerd/containerd/integration/images"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/platforms"
)

var (
	noDaemon     bool
	noCriu       bool
	supportsCriu bool
	noShimCgroup bool
)

func init() {
	flag.BoolVar(&noDaemon, "no-daemon", false, "Do not start a dedicated daemon for the tests")
	flag.BoolVar(&noCriu, "no-criu", false, "Do not run the checkpoint tests")
	flag.BoolVar(&noShimCgroup, "no-shim-cgroup", false, "Do not run the shim cgroup tests")
}

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	testutil.RequiresRootM()
	// check if criu is installed on the system
	_, err := exec.LookPath("criu")
	supportsCriu = err == nil && !noCriu

	var (
		buf         = bytes.NewBuffer(nil)
		ctx, cancel = testContext(nil)
	)
	defer cancel()

	if !noDaemon {
		_ = forceRemoveAll(defaultRoot)

		stdioFile, err := os.CreateTemp("", "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not create a new stdio temp file: %s\n", err)
			os.Exit(1)
		}
		defer func() {
			stdioFile.Close()
			os.Remove(stdioFile.Name())
		}()
		ctrdStdioFilePath = stdioFile.Name()
		stdioWriter := io.MultiWriter(stdioFile, buf)

		err = ctrd.start("containerd", address, []string{
			"--root", defaultRoot,
			"--state", defaultState,
			"--log-level", "debug",
			"--config", createShimDebugConfig(),
		}, stdioWriter, stdioWriter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", err, buf.String())
			os.Exit(1)
		}
	} else {
		// Otherwise if no-daemon was specified we need to connect to an already running ctrd instance.
		// Set the addr field on the daemon object so it knows what to try connecting to.
		ctrd.addr = address
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 4*time.Second)
	client, err := ctrd.waitForStart(waitCtx)
	waitCancel()
	if err != nil {
		ctrd.Kill()
		ctrd.Wait()
		fmt.Fprintf(os.Stderr, "%s: %s\n", err, buf.String())
		os.Exit(1)
	}

	// print out the version in information
	version, err := client.Version(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting version: %s\n", err)
		os.Exit(1)
	}

	// allow comparison with containerd under test
	log.G(ctx).WithFields(logrus.Fields{
		"version":     version.Version,
		"revision":    version.Revision,
		"runtime":     os.Getenv("TEST_RUNTIME"),
		"snapshotter": os.Getenv("TEST_SNAPSHOTTER"),
	}).Info("running tests against containerd")

	snapshotter := DefaultSnapshotter
	if ss := os.Getenv("TEST_SNAPSHOTTER"); ss != "" {
		snapshotter = ss
	}

	ns, ok := namespaces.Namespace(ctx)
	if !ok {
		fmt.Fprintln(os.Stderr, "error getting namespace")
		os.Exit(1)
	}
	err = client.NamespaceService().SetLabel(ctx, ns, defaults.DefaultSnapshotterNSLabel, snapshotter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error setting %s's default snapshotter as %s: %s\n", ns, snapshotter, err)
		os.Exit(1)
	}

	testSnapshotter = snapshotter

	// pull a seed image
	log.G(ctx).WithField("image", testImage).Info("start to pull seed image")
	if _, err = client.Pull(ctx, testImage, WithPullUnpack); err != nil {
		ctrd.Kill()
		ctrd.Wait()
		fmt.Fprintf(os.Stderr, "%s: %s\n", err, buf.String())
		os.Exit(1)
	}

	if err := client.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "failed to close client", err)
	}

	// run the test
	status := m.Run()

	if !noDaemon {
		// tear down the daemon and resources created
		if err := ctrd.Stop(); err != nil {
			if err := ctrd.Kill(); err != nil {
				fmt.Fprintln(os.Stderr, "failed to signal containerd", err)
			}
		}
		if err := ctrd.Wait(); err != nil {
			if _, ok := err.(*exec.ExitError); !ok {
				fmt.Fprintln(os.Stderr, "failed to wait for containerd", err)
			}
		}

		if err := forceRemoveAll(defaultRoot); err != nil {
			fmt.Fprintln(os.Stderr, "failed to remove test root dir", err)
			os.Exit(1)
		}

		// only print containerd logs if the test failed or tests were run with -v
		if status != 0 || testing.Verbose() {
			fmt.Fprintln(os.Stderr, buf.String())
		}
	}
	os.Exit(status)
}

func newClient(t testing.TB, address string, opts ...ClientOpt) (*Client, error) {
	if testing.Short() {
		t.Skip()
	}
	if rt := os.Getenv("TEST_RUNTIME"); rt != "" {
		opts = append(opts, WithDefaultRuntime(rt))
	}
	// testutil.RequiresRoot(t) is not needed here (already called in TestMain)
	return New(address, opts...)
}

func TestNewClient(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("New() returned nil client")
	}
	if err := client.Close(); err != nil {
		t.Errorf("client closed returned error %v", err)
	}
}

// All the container's tests depends on this, we need it to run first.
func TestImagePull(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()
	_, err = client.Pull(ctx, testImage, WithPlatformMatcher(platforms.Default()))
	if err != nil {
		t.Fatal(err)
	}
}

func TestImagePullWithDiscardContent(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()

	err = client.ImageService().Delete(ctx, testImage, images.SynchronousDelete())
	if err != nil {
		t.Fatal(err)
	}

	ls := client.LeasesService()
	l, err := ls.Create(ctx, leases.WithRandomID(), leases.WithExpiration(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ctx = leases.WithLease(ctx, l.ID)
	img, err := client.Pull(ctx, testImage,
		WithPlatformMatcher(platforms.Default()),
		WithPullUnpack,
		WithChildLabelMap(images.ChildGCLabelsFilterLayers),
	)
	// Synchronously garbage collect contents
	if errL := ls.Delete(ctx, l, leases.SynchronousDelete); errL != nil {
		t.Fatal(errL)
	}
	if err != nil {
		t.Fatal(err)
	}

	// Check if all layer contents have been unpacked and aren't preserved
	var (
		diffIDs []digest.Digest
		layers  []digest.Digest
	)
	cs := client.ContentStore()
	manifest, err := images.Manifest(ctx, cs, img.Target(), platforms.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Layers) == 0 {
		t.Fatalf("failed to get children from %v", img.Target())
	}
	for _, l := range manifest.Layers {
		layers = append(layers, l.Digest)
	}
	config, err := images.Config(ctx, cs, img.Target(), platforms.Default())
	if err != nil {
		t.Fatal(err)
	}
	diffIDs, err = images.RootFS(ctx, cs, config)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != len(diffIDs) {
		t.Fatalf("number of layers and diffIDs don't match: %d != %d", len(layers), len(diffIDs))
	} else if len(layers) == 0 {
		t.Fatalf("there is no layers in the target image(parent: %v)", img.Target())
	}
	var (
		sn    = client.SnapshotService(testSnapshotter)
		chain []digest.Digest
	)
	for i, dgst := range layers {
		chain = append(chain, diffIDs[i])
		chainID := identity.ChainID(chain).String()
		if _, err := sn.Stat(ctx, chainID); err != nil {
			t.Errorf("snapshot %v must exist: %v", chainID, err)
		}
		if _, err := cs.Info(ctx, dgst); err == nil || !errdefs.IsNotFound(err) {
			t.Errorf("content %v must be garbage collected: %v", dgst, err)
		}
	}
}

func TestImagePullAllPlatforms(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := testContext(t)
	defer cancel()

	cs := client.ContentStore()
	img, err := client.Fetch(ctx, imagelist.Get(imagelist.Pause))
	if err != nil {
		t.Fatal(err)
	}
	index := img.Target
	manifests, err := images.Children(ctx, cs, index)
	if err != nil {
		t.Fatal(err)
	}
	for _, manifest := range manifests {
		children, err := images.Children(ctx, cs, manifest)
		if err != nil {
			t.Fatal("Th")
		}
		// check if childless data type has blob in content store
		for _, desc := range children {
			ra, err := cs.ReaderAt(ctx, desc)
			if err != nil {
				t.Fatal(err)
			}
			ra.Close()
		}
	}
}

func TestImagePullSomePlatforms(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := testContext(t)
	defer cancel()

	cs := client.ContentStore()
	platformList := []string{"linux/amd64", "linux/arm64/v8", "linux/s390x"}
	m := make(map[string]platforms.Matcher)
	var opts []RemoteOpt

	for _, platform := range platformList {
		p, err := platforms.Parse(platform)
		if err != nil {
			t.Fatal(err)
		}
		m[platform] = platforms.NewMatcher(p)
		opts = append(opts, WithPlatform(platform))
	}

	// Note: Must be different to the image used in TestImagePullAllPlatforms
	// or it will see the content pulled by that, and fail.
	img, err := client.Fetch(ctx, "registry.k8s.io/e2e-test-images/busybox:1.29-2", opts...)
	if err != nil {
		t.Fatal(err)
	}

	index := img.Target
	manifests, err := images.Children(ctx, cs, index)
	if err != nil {
		t.Fatal(err)
	}

	count := 0
	for _, manifest := range manifests {
		children, err := images.Children(ctx, cs, manifest)

		found := false
		for _, matcher := range m {
			if manifest.Platform == nil {
				t.Fatal("manifest should have proper platform")
			}
			if matcher.Match(*manifest.Platform) {
				count++
				found = true
			}
		}

		if found {
			if len(children) == 0 {
				t.Fatal("manifest should have pulled children content")
			}

			// check if childless data type has blob in content store
			for _, desc := range children {
				ra, err := cs.ReaderAt(ctx, desc)
				if err != nil {
					t.Fatal(err)
				}
				ra.Close()
			}
		} else if err == nil {
			t.Fatal("manifest should not have pulled children content")
		}
	}

	if count != len(platformList) {
		t.Fatal("expected a different number of pulled manifests")
	}
}

func TestImagePullSchema1(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()
	schema1TestImage := "gcr.io/google_containers/pause:3.0@sha256:0d093c962a6c2dd8bb8727b661e2b5f13e9df884af9945b4cc7088d9350cd3ee"
	_, err = client.Pull(ctx, schema1TestImage, WithPlatform(platforms.DefaultString()), WithSchema1Conversion)
	if err != nil {
		t.Fatal(err)
	}
}

func TestImagePullWithConcurrencyLimit(t *testing.T) {
	if os.Getenv("CIRRUS_CI") != "" {
		// This test tends to fail under Cirrus CI + Vagrant due to "connection reset by peer" from
		// pkg-containers.githubusercontent.com.
		// Does GitHub throttle requests from Cirrus CI more compared to GitHub Actions?
		t.Skip("unstable under Cirrus CI")
	}

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()
	_, err = client.Pull(ctx, testImage,
		WithPlatformMatcher(platforms.Default()),
		WithMaxConcurrentDownloads(2))
	if err != nil {
		t.Fatal(err)
	}
}

func TestImagePullWithTracing(t *testing.T) {
	client, err := newClient(t, address)
	require.NoError(t, err)
	defer client.Close()

	ctx := namespaces.WithNamespace(context.Background(), "tracing")

	//create in memory exporter and global tracer provider for test
	exp, tp := newInMemoryExporterTracer()
	//set the tracer provider global available
	otel.SetTracerProvider(tp)
	// Shutdown properly so nothing leaks.
	defer func() { _ = tp.Shutdown(ctx) }()

	//do an image pull which is instrumented, we should expect spans in the exporter
	_, err = client.Pull(ctx, testImage, WithPlatformMatcher(platforms.Default()))
	require.NoError(t, err)

	err = tp.ForceFlush(ctx)
	require.NoError(t, err)

	//The span name was defined in client.pull when instrumented it
	spanNameExpected := "pull.Pull"
	spans := exp.GetSpans()
	validateRootSpan(t, spanNameExpected, spans)

}

func TestClientReconnect(t *testing.T) {
	t.Parallel()

	ctx, cancel := testContext(t)
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("New() returned nil client")
	}
	ok, err := client.IsServing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("containerd is not serving")
	}
	if err := client.Reconnect(); err != nil {
		t.Fatal(err)
	}
	if ok, err = client.IsServing(ctx); err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("containerd is not serving")
	}
	if err := client.Close(); err != nil {
		t.Errorf("client closed returned error %v", err)
	}
}

func TestDefaultRuntimeWithNamespaceLabels(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()
	namespaces := client.NamespaceService()
	testRuntime := "testRuntime"
	runtimeLabel := defaults.DefaultRuntimeNSLabel
	if err := namespaces.SetLabel(ctx, testNamespace, runtimeLabel, testRuntime); err != nil {
		t.Fatal(err)
	}

	testClient, err := New(address, WithDefaultNamespace(testNamespace))
	if err != nil {
		t.Fatal(err)
	}
	defer testClient.Close()
	if testClient.Runtime() != testRuntime {
		t.Error("failed to set default runtime from namespace labels")
	}
}
