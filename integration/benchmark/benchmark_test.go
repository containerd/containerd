package benchmark

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/log/logtest"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/testutil"
)

var (
	address       = "/run/containerd/containerd.sock"
	testImage     = "docker.io/library/python:3"
	testNamespace = "testing"
)

// Called once to set up and tear down for all the benchmarks in this file
func TestMain(m *testing.M) {
	// testutil.RequiresRootM() // FIXME does not respect -test.root flag
	// setup steps prior to all test runs
	client, err := testClient(nil, address)
	if err != nil {
		fmt.Printf("Error testClient : %s\n", err)
		return
	}

	ctx, cancel := testContext(nil)
	defer cancel()

	_, err = client.Pull(ctx, testImage, containerd.WithPullUnpack)
	if err != nil {
		fmt.Printf("Error Pull : %s\n", err)
		return
	}

	// perform the actual benchmark runs
	os.Exit(m.Run())

	// teardown steps go here
}

func BenchmarkContainerLifecycle(b *testing.B) {
	testutil.RequiresRoot(b)
	client, err := testClient(b, address)
	if err != nil {
		b.Fatalf("Error new client : %s\n", err)
	}
	defer client.Close()

	ctx, cancel := testContext(b)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		b.Fatalf("Error GetImage : %s\n", err)
		return
	}

	container, err := client.NewContainer(ctx, "test-container",
		containerd.WithImage(image),
		containerd.WithNewSnapshot("test-snapshot", image),
		containerd.WithNewSpec(
			oci.WithImageConfig(image),
			oci.WithProcessArgs("python3", "-c", "print('Hello World')"),
		))
	if err != nil {
		b.Fatalf("Error NewContainer : %s\n", err)
		return
	}

	defer container.Delete(ctx, containerd.WithSnapshotCleanup)

	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStreams(nil, os.Stdout, os.Stderr)))
	if err != nil {
		b.Fatalf("Error NewTask : %s\n", err)
		return
	}

	defer task.Delete(ctx)

	exitC, err := task.Wait(ctx)
	if err != nil {
		b.Fatalf("Error Wait : %s\n", err)
		return
	}

	err = task.Start(ctx)
	if err != nil {
		b.Fatalf("Error Start : %s\n", err)
		return
	}

	<-exitC
}

func testClient(t testing.TB, address string, opts ...containerd.ClientOpt) (*containerd.Client, error) {
	if rt := os.Getenv("TEST_RUNTIME"); rt != "" {
		opts = append(opts, containerd.WithDefaultRuntime(rt))
	}
	if t != nil && testing.Short() {
		t.Skip()
	}
	return containerd.New(address, opts...)
}

func testContext(t testing.TB) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, testNamespace)
	if t != nil {
		ctx = logtest.WithT(ctx, t)
	}
	return ctx, cancel
}
