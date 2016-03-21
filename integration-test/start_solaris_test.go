package main

import (
	"path/filepath"
	"syscall"
	"time"

	"github.com/docker/containerd/api/grpc/types"
	"github.com/docker/docker/pkg/integration/checker"
	"github.com/go-check/check"
	"google.golang.org/grpc"
)

func (cs *ContainerdSuite) TestStartBusyboxLsSlash(t *check.C) {
	expectedOutput := `bin
dev
etc
export
home
lib
mnt
opt
proc
root
sbin
system
tmp
usr
var
`
	if err := CreateSolarisBundle("busybox-ls-slash", []string{"ls", "/"}); err != nil {
		t.Fatal(err)
	}

	c, err := cs.RunContainer("myls", "busybox-ls-slash")
	if err != nil {
		t.Fatal(err)
	}

	t.Assert(c.io.stdoutBuffer.String(), checker.Equals, expectedOutput)
}

func (cs *ContainerdSuite) TestStartBusyboxNoSuchFile(t *check.C) {
	expectedOutput := `NoSuchFile: No such file or directory`

	if err := CreateSolarisBundle("busybox-no-such-file", []string{"NoSuchFile"}); err != nil {
		t.Fatal(err)
	}

	_, err := cs.RunContainer("NoSuchFile", "busybox-no-such-file")
	t.Assert(grpc.ErrorDesc(err), checker.Contains, expectedOutput)
}

func (cs *ContainerdSuite) TestStartBusyboxTop(t *check.C) {
	bundleName := "busybox-top"
	if err := CreateSolarisBundle(bundleName, []string{"sleep", "10"}); err != nil {
		t.Fatal(err)
	}

	containerID := "start-busybox-top"
	_, err := cs.StartContainer(containerID, bundleName)
	t.Assert(err, checker.Equals, nil)

	containers, err := cs.ListRunningContainers()
	if err != nil {
		t.Fatal(err)
	}
	t.Assert(len(containers), checker.Equals, 1)
	t.Assert(containers[0].Id, checker.Equals, containerID)
	t.Assert(containers[0].Status, checker.Equals, "running")
	t.Assert(containers[0].BundlePath, check.Equals, filepath.Join(cs.cwd, GetBundle(bundleName).Path))
}

func (cs *ContainerdSuite) TestStartBusyboxLsEvents(t *check.C) {
	if err := CreateSolarisBundle("busybox-ls", []string{"ls"}); err != nil {
		t.Fatal(err)
	}

	containerID := "ls-events"
	c, err := cs.StartContainer(containerID, "busybox-ls")
	if err != nil {
		t.Fatal(err)
	}

	for _, evt := range []types.Event{
		{
			Type:   "start-container",
			Id:     containerID,
			Status: 0,
			Pid:    "",
		},
		{
			Type:   "exit",
			Id:     containerID,
			Status: 0,
			Pid:    "init",
		},
	} {
		ch := c.GetEventsChannel()
		select {
		case e := <-ch:
			evt.Timestamp = e.Timestamp
			evt.Status = e.Status

			t.Assert(*e, checker.Equals, evt)
		case <-time.After(2 * time.Second):
			t.Fatal("Container took more than 2 seconds to terminate")
		}
	}
}

func (cs *ContainerdSuite) TestStartBusyboxSleep(t *check.C) {
	if err := CreateSolarisBundle("busybox-sleep-5", []string{"sleep", "5"}); err != nil {
		t.Fatal(err)
	}

	ch := make(chan interface{})
	filter := func(e *types.Event) {
		if e.Type == "exit" && e.Pid == "init" {
			ch <- nil
		}
	}

	start := time.Now()
	_, err := cs.StartContainerWithEventFilter("sleep5", "busybox-sleep-5", filter)
	if err != nil {
		t.Fatal(err)
	}

	// We add a generous 20% marge of error
	select {
	case <-ch:
		t.Assert(uint64(time.Now().Sub(start)), checker.LessOrEqualThan, uint64(15*time.Second))
	case <-time.After(15 * time.Second):
		t.Fatal("Container took more than 15 seconds to exit")
	}
}

func (cs *ContainerdSuite) TestStartBusyboxTopKill(t *check.C) {
	bundleName := "busybox-top"
	if err := CreateSolarisBundle(bundleName, []string{"sleep", "10"}); err != nil {
		t.Fatal(err)
	}

	containerID := "top-kill"
	c, err := cs.StartContainer(containerID, bundleName)
	if err != nil {
		t.Fatal(err)
	}

	<-time.After(5 * time.Second)

	err = cs.KillContainer(containerID)
	if err != nil {
		t.Fatal(err)
	}

	for _, evt := range []types.Event{
		{
			Type:   "start-container",
			Id:     containerID,
			Status: 0,
			Pid:    "",
		},
		{
			Type:   "exit",
			Id:     containerID,
			Status: 128 + uint32(syscall.SIGKILL),
			Pid:    "init",
		},
	} {
		ch := c.GetEventsChannel()
		select {
		case e := <-ch:
			evt.Timestamp = e.Timestamp
			evt.Status = e.Status

			t.Assert(*e, checker.Equals, evt)
		case <-time.After(2 * time.Second):
			t.Fatal("Container took more than 2 seconds to terminate")
		}
	}
}

func (cs *ContainerdSuite) TestStartBusyboxTopSignalSigterm(t *check.C) {
	bundleName := "busybox-top"
	if err := CreateSolarisBundle(bundleName, []string{"sleep", "10"}); err != nil {
		t.Fatal(err)
	}

	containerID := "top-sigterm"
	c, err := cs.StartContainer(containerID, bundleName)
	if err != nil {
		t.Fatal(err)
	}

	<-time.After(5 * time.Second)

	err = cs.SignalContainer(containerID, uint32(syscall.SIGTERM))
	if err != nil {
		t.Fatal(err)
	}

	for _, evt := range []types.Event{
		{
			Type:   "start-container",
			Id:     containerID,
			Status: 0,
			Pid:    "",
		},
		{
			Type:   "exit",
			Id:     containerID,
			Status: 128 + uint32(syscall.SIGTERM),
			Pid:    "init",
		},
	} {
		ch := c.GetEventsChannel()
		select {
		case e := <-ch:
			evt.Timestamp = e.Timestamp
			evt.Status = e.Status

			t.Assert(*e, checker.Equals, evt)
		case <-time.After(2 * time.Second):
			t.Fatal("Container took more than 2 seconds to terminate")
		}
	}
}
