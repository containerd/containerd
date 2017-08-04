package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/api/grpc/types"
	"github.com/containerd/containerd/runtime"
	"github.com/docker/docker/integration-cli/checker"
	"github.com/go-check/check"
)

func (cs *ContainerdSuite) TestRestoreWithNoPRocessDir(t *check.C) {
	bundleName := "busybox-top"
	if err := CreateBusyboxBundle(bundleName, []string{"top"}); err != nil {
		t.Fatal(err)
	}

	containerID := "temp-top"
	c, err := cs.StartContainer(containerID, bundleName)
	t.Assert(err, checker.Equals, nil)

	ch := c.GetEventsChannel()
	select {
	case e := <-ch:
		t.Assert(*e, checker.Equals, types.Event{
			Type:      "start-container",
			Id:        containerID,
			Status:    0,
			Pid:       "",
			Timestamp: e.Timestamp,
		})
	case <-time.After(2 * time.Second):
		t.Fatal("Container took more than 2 seconds to start")
	}

	// Kill containerd
	cs.StopDaemon(true)

	// Kill Top
	err = exec.Command("pkill", "-9", "top").Run()
	t.Assert(err, checker.Equals, nil)

	// Delete the init process directory
	err = os.RemoveAll(filepath.Join(cs.stateDir, containerID, runtime.InitProcessID))
	t.Assert(err, checker.Equals, nil)

	// restart daemon and make sure we get an exit event
	cs.StartDaemon()

	containers, err := cs.ListRunningContainers()
	t.Assert(err, checker.Equals, nil)
	t.Assert(len(containers), checker.Equals, 0)
}
