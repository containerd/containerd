package containerd

import (
	"context"
	"testing"
)

func TestContainerList(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	containers, err := client.Containers(context.Background())
	if err != nil {
		t.Errorf("container list returned error %v", err)
		return
	}
	if len(containers) != 0 {
		t.Errorf("expected 0 containers but received %d", len(containers))
	}
}

func TestNewContainer(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	id := "test"
	spec, err := GenerateSpec()
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
	if err := container.Delete(context.Background()); err != nil {
		t.Error(err)
		return
	}
}
