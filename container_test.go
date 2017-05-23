package containerd

import (
	"context"
	"testing"
)

func TestContainerList(t *testing.T) {
	client, err := New(defaultAddress)
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
