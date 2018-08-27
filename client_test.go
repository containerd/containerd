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

package containerd

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/sys"
	"github.com/sirupsen/logrus"
)

var (
	address       string
	noDaemon      bool
	noCriu        bool
	supportsCriu  bool
	testNamespace = "testing"

	ctrd = &daemon{}
)

func init() {
	flag.StringVar(&address, "address", defaultAddress, "The address to the containerd socket for use in the tests")
	flag.BoolVar(&noDaemon, "no-daemon", false, "Do not start a dedicated daemon for the tests")
	flag.BoolVar(&noCriu, "no-criu", false, "Do not run the checkpoint tests")
	flag.Parse()
}

func testContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, testNamespace)
	return ctx, cancel
}

func TestMain(m *testing.M) {
	if testing.Short() {
		os.Exit(m.Run())
	}
	testutil.RequiresRootM()
	// check if criu is installed on the system
	_, err := exec.LookPath("criu")
	supportsCriu = err == nil && !noCriu

	var (
		buf         = bytes.NewBuffer(nil)
		ctx, cancel = testContext()
	)
	defer cancel()

	if !noDaemon {
		sys.ForceRemoveAll(defaultRoot)

		err := ctrd.start("containerd", address, []string{
			"--root", defaultRoot,
			"--state", defaultState,
			"--log-level", "debug",
		}, buf, buf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s", err, buf.String())
			os.Exit(1)
		}
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Second)
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
		"version":  version.Version,
		"revision": version.Revision,
	}).Info("running tests against containerd")

	// pull a seed image
	if _, err = client.Pull(ctx, testImage, WithPullUnpack); err != nil {
		ctrd.Stop()
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
		if err := sys.ForceRemoveAll(defaultRoot); err != nil {
			fmt.Fprintln(os.Stderr, "failed to remove test root dir", err)
			os.Exit(1)
		}
		// only print containerd logs if the test failed
		if status != 0 {
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
		t.Errorf("client closed returned errror %v", err)
	}
}

// All the container's tests depends on this, we need it to run first.
func TestImagePull(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext()
	defer cancel()
	_, err = client.Pull(ctx, testImage, WithPlatformMatcher(platforms.Default()))
	if err != nil {
		t.Fatal(err)
	}
}

func TestImagePullAllPlatforms(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := testContext()
	defer cancel()

	cs := client.ContentStore()
	img, err := client.Fetch(ctx, "docker.io/library/busybox:latest")
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
	ctx, cancel := testContext()
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

	img, err := client.Fetch(ctx, "k8s.gcr.io/pause:3.1", opts...)
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
		} else if !found && err == nil {
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

	ctx, cancel := testContext()
	defer cancel()
	schema1TestImage := "gcr.io/google_containers/pause:3.0@sha256:0d093c962a6c2dd8bb8727b661e2b5f13e9df884af9945b4cc7088d9350cd3ee"
	_, err = client.Pull(ctx, schema1TestImage, WithPlatform(platforms.DefaultString()), WithSchema1Conversion)
	if err != nil {
		t.Fatal(err)
	}
}

func TestClientReconnect(t *testing.T) {
	t.Parallel()

	ctx, cancel := testContext()
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
		t.Errorf("client closed returned errror %v", err)
	}
}
