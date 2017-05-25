package containerd

import (
	"bytes"
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
	id := "NewContainer"
	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

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
	defer container.Delete(context.Background())
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

func TestContainerStart(t *testing.T) {
	if testing.Short() {
		t.Skip()
		return
	}
	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		ctx = context.Background()
		id  = "ContainerStart"
	)
	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Error(err)
		return
	}
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("sh", "-c", "exit 7"))
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id, spec, WithImage(image), WithNewRootFS(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx)

	task, err := container.NewTask(ctx, Stdio)
	if err != nil {
		t.Error(err)
		return
	}
	defer task.Delete(ctx)

	statusC := make(chan uint32, 1)
	go func() {
		status, err := task.Wait(ctx)
		if err != nil {
			t.Error(err)
		}
		statusC <- status
	}()

	if pid := task.Pid(); pid <= 0 {
		t.Errorf("invalid task pid %d", pid)
	}
	if err := task.Start(ctx); err != nil {
		t.Error(err)
		task.Delete(ctx)
		return
	}
	status := <-statusC
	if status != 7 {
		t.Errorf("expected status 7 from wait but received %d", status)
	}
	if status, err = task.Delete(ctx); err != nil {
		t.Error(err)
		return
	}
	if status != 7 {
		t.Errorf("expected status 7 from delete but received %d", status)
	}
}

func TestContainerOutput(t *testing.T) {
	if testing.Short() {
		t.Skip()
		return
	}
	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		ctx      = context.Background()
		id       = "ContainerOutput"
		expected = "kingkoye"
	)
	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Error(err)
		return
	}
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("echo", expected))
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id, spec, WithImage(image), WithNewRootFS(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx)

	stdout := bytes.NewBuffer(nil)
	task, err := container.NewTask(ctx, BufferedIO(bytes.NewBuffer(nil), stdout, bytes.NewBuffer(nil)))
	if err != nil {
		t.Error(err)
		return
	}
	defer task.Delete(ctx)

	statusC := make(chan uint32, 1)
	go func() {
		status, err := task.Wait(ctx)
		if err != nil {
			t.Error(err)
		}
		statusC <- status
	}()

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	status := <-statusC
	if status != 0 {
		t.Errorf("expected status 0 but received %d", status)
	}
	if _, err := task.Delete(ctx); err != nil {
		t.Error(err)
		return
	}

	actual := stdout.String()
	// echo adds a new line
	expected = expected + "\n"
	if actual != expected {
		t.Errorf("expected output %q but received %q", expected, actual)
	}
}
