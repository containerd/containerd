package containerd

import (
	"context"
	"testing"
)

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

func TestNewContainer(t *testing.T) {
	client, err := New(defaultAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	id := "test"
	spec, err := GenerateSpec(WithHostname(id))
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(context.Background(), id, spec)
	if err != nil {
		t.Error(err)
		return
	}
	if container.ID() != id {
		t.Errorf("expected container id %q but received %q", id, container.ID())
	}
	if spec, err = container.Spec(); err != nil {
		t.Error(err)
		return
	}
	if spec.Hostname != id {
		t.Errorf("expected spec hostname id %q but received %q", id, container.ID())
		return
	}
	if err := container.Delete(context.Background()); err != nil {
		t.Error(err)
		return
	}
}
