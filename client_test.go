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
	"io/ioutil"
	golog "log"
	"os"
	"os/exec"
	"testing"
	"time"

	"google.golang.org/grpc/grpclog"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/sys"
	"github.com/containerd/containerd/testutil"
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
	// Discard grpc logs so that they don't mess with our stdio
	grpclog.SetLogger(golog.New(ioutil.Discard, "", golog.LstdFlags))

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
	_, err = client.Pull(ctx, testImage)
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
