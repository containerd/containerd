package containerd

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/namespaces"
)

const (
	defaultRoot  = "/var/lib/containerd-test"
	defaultState = "/run/containerd-test"
	testImage    = "docker.io/library/alpine:latest"
)

var (
	address      string
	noDaemon     bool
	supportsCriu bool
)

func init() {
	flag.StringVar(&address, "address", "/run/containerd/containerd.sock", "The address to the containerd socket for use in the tests")
	flag.BoolVar(&noDaemon, "no-daemon", false, "Do not start a dedicated daemon for the tests")
	flag.Parse()
}

func testContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, "testing")
	return ctx, cancel
}

func TestMain(m *testing.M) {
	if testing.Short() {
		os.Exit(m.Run())
	}

	// check if criu is installed on the system
	_, err := exec.LookPath("criu")
	supportsCriu = err == nil

	var (
		cmd         *exec.Cmd
		buf         = bytes.NewBuffer(nil)
		ctx, cancel = testContext()
	)
	defer cancel()

	if !noDaemon {
		// setup a new containerd daemon if !testing.Short
		cmd = exec.Command("containerd",
			"--root", defaultRoot,
			"--state", defaultState,
		)
		cmd.Stderr = buf
		if err := cmd.Start(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	client, err := New(address)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := waitForDaemonStart(ctx, client); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// pull a seed image
	if _, err = client.Pull(ctx, testImage, WithPullUnpack); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)

	}
	if err := client.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}

	// run the test
	status := m.Run()

	if !noDaemon {
		// tear down the daemon and resources created
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		if _, err := cmd.Process.Wait(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		if err := os.RemoveAll(defaultRoot); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		// only print containerd logs if the test failed
		if status != 0 {
			fmt.Fprintln(os.Stderr, buf.String())
		}
	}
	os.Exit(status)
}

func waitForDaemonStart(ctx context.Context, client *Client) error {
	var (
		serving bool
		err     error
	)

	for i := 0; i < 20; i++ {
		serving, err = client.IsServing(ctx)
		if serving {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("containerd did not start within 2s: %v", err)
}

func TestNewClient(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	client, err := New(address)
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

func TestImagePull(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx, cancel := testContext()
	defer cancel()

	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.Pull(ctx, testImage)
	if err != nil {
		t.Error(err)
		return
	}
}
