package containerd

import (
	"context"
	"flag"
	"testing"
)

func init() {
	flag.StringVar(&address, "address", "/run/containerd/containerd.sock", "The address to the containerd socket for use in the tests")
	flag.Parse()
}

var address string

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
	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	const ref = "docker.io/library/alpine:latest"
	_, err = client.Pull(context.Background(), ref)
	if err != nil {
		t.Error(err)
		return
	}
}
