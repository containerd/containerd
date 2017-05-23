package containerd

import "testing"

const defaultAddress = "/run/containerd/containerd.sock"

func TestNewClient(t *testing.T) {
	client, err := New(defaultAddress)
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
