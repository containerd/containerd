// +build !solaris

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd/api/grpc/types"
	"github.com/docker/docker/integration-cli/checker"
	"github.com/go-check/check"
)

func (cs *ContainerdSuite) TestBusyboxTopExecEcho(t *check.C) {
	bundleName := "busybox-top"
	if err := CreateBusyboxBundle(bundleName, []string{"top"}); err != nil {
		t.Fatal(err)
	}

	var (
		err   error
		initp *ContainerProcess
		echop *ContainerProcess
	)

	containerID := "top"
	initp, err = cs.StartContainer(containerID, bundleName)
	t.Assert(err, checker.Equals, nil)

	echop, err = cs.AddProcessToContainer(initp, "echo", "/", []string{"PATH=/bin"}, []string{"sh", "-c", "echo -n Ay Caramba! ; exit 1"}, 0, 0)
	t.Assert(err, checker.Equals, nil)

	for _, evt := range []types.Event{
		{
			Type:   "start-container",
			Id:     containerID,
			Status: 0,
			Pid:    "",
		},
		{
			Type:   "start-process",
			Id:     containerID,
			Status: 0,
			Pid:    "echo",
		},
		{
			Type:   "exit",
			Id:     containerID,
			Status: 1,
			Pid:    "echo",
		},
	} {
		ch := initp.GetEventsChannel()
		e := <-ch
		evt.Timestamp = e.Timestamp

		t.Assert(*e, checker.Equals, evt)
	}

	t.Assert(echop.io.stdoutBuffer.String(), checker.Equals, "Ay Caramba!")
}

func (cs *ContainerdSuite) TestBusyboxTopExecTop(t *check.C) {
	bundleName := "busybox-top"
	if err := CreateBusyboxBundle(bundleName, []string{"top"}); err != nil {
		t.Fatal(err)
	}

	var (
		err   error
		initp *ContainerProcess
	)

	containerID := "top"
	initp, err = cs.StartContainer(containerID, bundleName)
	t.Assert(err, checker.Equals, nil)

	execID := "top1"
	_, err = cs.AddProcessToContainer(initp, execID, "/", []string{"PATH=/usr/bin"}, []string{"top"}, 0, 0)
	t.Assert(err, checker.Equals, nil)

	for idx, evt := range []types.Event{
		{
			Type:   "start-container",
			Id:     containerID,
			Status: 0,
			Pid:    "",
		},
		{
			Type:   "start-process",
			Id:     containerID,
			Status: 0,
			Pid:    execID,
		},
		{
			Type:   "exit",
			Id:     containerID,
			Status: 137,
			Pid:    execID,
		},
	} {
		ch := initp.GetEventsChannel()
		e := <-ch
		evt.Timestamp = e.Timestamp
		t.Assert(*e, checker.Equals, evt)
		if idx == 1 {
			// Process Started, kill it
			cs.SignalContainerProcess(containerID, "top1", uint32(syscall.SIGKILL))
		}
	}

	// Container should still be running
	containers, err := cs.ListRunningContainers()
	if err != nil {
		t.Fatal(err)
	}
	t.Assert(len(containers), checker.Equals, 1)
	t.Assert(containers[0].Id, checker.Equals, "top")
	t.Assert(containers[0].Status, checker.Equals, "running")
	t.Assert(containers[0].BundlePath, check.Equals, filepath.Join(cs.cwd, GetBundle(bundleName).Path))
}

func (cs *ContainerdSuite) TestBusyboxTopExecTopKillInit(t *check.C) {
	bundleName := "busybox-top"
	if err := CreateBusyboxBundle(bundleName, []string{"top"}); err != nil {
		t.Fatal(err)
	}

	var (
		err   error
		initp *ContainerProcess
	)

	containerID := "top"
	initp, err = cs.StartContainer(containerID, bundleName)
	t.Assert(err, checker.Equals, nil)

	execID := "top1"
	_, err = cs.AddProcessToContainer(initp, execID, "/", []string{"PATH=/usr/bin"}, []string{"top"}, 0, 0)
	t.Assert(err, checker.Equals, nil)

	ch := initp.GetEventsChannel()
	for _, evt := range []types.Event{
		{
			Type:   "start-container",
			Id:     containerID,
			Status: 0,
			Pid:    "",
		},
		{
			Type:   "start-process",
			Id:     containerID,
			Status: 0,
			Pid:    execID,
		},
	} {
		e := <-ch
		evt.Timestamp = e.Timestamp
		t.Assert(*e, checker.Equals, evt)
	}

	cs.SignalContainerProcess(containerID, "init", uint32(syscall.SIGTERM))
	for i := 0; i < 2; i++ {
		e := <-ch
		switch e.Pid {
		case "init":
			evt := types.Event{
				Type:      "exit",
				Id:        containerID,
				Status:    143,
				Pid:       "init",
				Timestamp: e.Timestamp,
			}
			t.Assert(*e, checker.Equals, evt)
		case execID:
			evt := types.Event{
				Type:      "exit",
				Id:        containerID,
				Status:    137,
				Pid:       execID,
				Timestamp: e.Timestamp,
			}
			t.Assert(*e, checker.Equals, evt)
		default:
			t.Fatalf("Unexpected event %v", e)
		}
	}

}

func (cs *ContainerdSuite) TestBusyboxExecCreateDetachedChild(t *check.C) {
	bundleName := "busybox-top"
	if err := CreateBusyboxBundle(bundleName, []string{"top"}); err != nil {
		t.Fatal(err)
	}

	var (
		err   error
		initp *ContainerProcess
	)

	containerID := "top"
	initp, err = cs.StartContainer(containerID, bundleName)
	t.Assert(err, checker.Equals, nil)

	ch := initp.GetEventsChannel()
	for _, evt := range []types.Event{
		{
			Type:   "start-container",
			Id:     containerID,
			Status: 0,
			Pid:    "",
		},
	} {
		e := <-ch
		evt.Timestamp = e.Timestamp
		t.Assert(*e, checker.Equals, evt)
	}

	execID := "sh-sleep"
	_, err = cs.AddProcessToContainer(initp, execID, "/", []string{"PATH=/bin"}, []string{"sh", "-c", "sleep 1000 2>&- 1>&- 0<&- &"}, 0, 0)
	t.Assert(err, checker.Equals, nil)
	for _, evt := range []types.Event{
		{
			Type:   "start-process",
			Id:     containerID,
			Status: 0,
			Pid:    execID,
		},
		{
			Type:   "exit",
			Id:     containerID,
			Status: 0,
			Pid:    execID,
		},
	} {
		e := <-ch
		evt.Timestamp = e.Timestamp
		t.Assert(*e, checker.Equals, evt)
	}

	// Check that sleep is still running
	execOutput, err := cs.AddProcessToContainer(initp, "ps", "/", []string{"PATH=/bin"}, []string{"ps", "aux"}, 0, 0)
	t.Assert(err, checker.Equals, nil)
	t.Assert(execOutput.io.stdoutBuffer.String(), checker.Contains, "sleep 1000")
}

func (cs *ContainerdSuite) TestBusyboxExecCreateAttachedChild(t *check.C) {
	bundleName := "busybox-top"
	if err := CreateBusyboxBundle(bundleName, []string{"top"}); err != nil {
		t.Fatal(err)
	}

	var (
		err   error
		initp *ContainerProcess
	)

	containerID := "top"
	initp, err = cs.StartContainer(containerID, bundleName)
	t.Assert(err, checker.Equals, nil)

	ch := initp.GetEventsChannel()
	for _, evt := range []types.Event{
		{
			Type:   "start-container",
			Id:     containerID,
			Status: 0,
			Pid:    "",
		},
	} {
		e := <-ch
		evt.Timestamp = e.Timestamp
		t.Assert(*e, checker.Equals, evt)
	}

	doneCh := make(chan struct{})
	go func() {
		execID := "sh-sleep"
		_, err = cs.AddProcessToContainer(initp, execID, "/", []string{"PATH=/bin"}, []string{"sh", "-c", "sleep 5 &"}, 0, 0)
		t.Assert(err, checker.Equals, nil)
		for _, evt := range []types.Event{
			{
				Type:   "start-process",
				Id:     containerID,
				Status: 0,
				Pid:    execID,
			},
			{
				Type:   "exit",
				Id:     containerID,
				Status: 0,
				Pid:    execID,
			},
		} {
			e := <-ch
			evt.Timestamp = e.Timestamp
			t.Assert(*e, checker.Equals, evt)
		}
		close(doneCh)
	}()

	select {
	case <-doneCh:
		break
	case <-time.After(8 * time.Second):
		t.Fatal("exec did not exit within 5 seconds")
	}
}

func (cs *ContainerdSuite) TestBusyboxRestoreDeadExec(t *check.C) {
	bundleName := "busybox-top"
	if err := CreateBusyboxBundle(bundleName, []string{"top"}); err != nil {
		t.Fatal(err)
	}

	var (
		err   error
		initp *ContainerProcess
	)

	containerID := "top"
	initp, err = cs.StartContainer(containerID, bundleName)
	t.Assert(err, checker.Equals, nil)

	ch := initp.GetEventsChannel()
	for _, evt := range []types.Event{
		{
			Type:   "start-container",
			Id:     containerID,
			Status: 0,
			Pid:    "",
		},
	} {
		e := <-ch
		evt.Timestamp = e.Timestamp
		t.Assert(*e, checker.Equals, evt)
	}

	createCh := make(chan struct{})
	killCh := make(chan struct{})
	doneCh := make(chan struct{})
	restartCh := make(chan struct{})
	go func() {
		execID := "sleep"
		p, err := cs.AddProcessToContainer(initp, execID, "/", []string{"PATH=/bin"}, []string{"sleep", "10"}, 0, 0)
		t.Assert(err, checker.Equals, nil)
		for idx, evt := range []types.Event{
			{
				Type:   "start-process",
				Id:     containerID,
				Status: 0,
				Pid:    execID,
			},
			{
				Type:   "exit",
				Id:     containerID,
				Status: 137,
				Pid:    execID,
			},
		} {
			e := <-ch
			evt.Timestamp = e.Timestamp
			t.Assert(*e, checker.Equals, evt)
			if idx == 0 {
				close(createCh)
				<-killCh
				syscall.Kill(int(p.systemPid), syscall.SIGKILL)
				close(restartCh)
			}
		}
		close(doneCh)
	}()

	// wait for sleep to be created
	<-createCh
	// stop containerd
	cs.StopDaemon(false)
	// notify go routine to go ahead and kill the sleep exec
	close(killCh)
	// wait for the kill signal to have been sent
	<-restartCh
	if err := cs.StartDaemon(); err != nil {
		t.Fatal(err)
	}
	// wait a tiny bit more just in case
	time.Sleep(100 * time.Millisecond)

	select {
	case <-doneCh:
		break
	case <-time.After(8 * time.Second):
		t.Fatal("exec did not exit within 5 seconds")
	}
}

func (cs *ContainerdSuite) TestExecKillShimNoPanic(t *check.C) {
	bundleName := "busybox-top"
	if err := CreateBusyboxBundle(bundleName, []string{"top"}); err != nil {
		t.Fatal(err)
	}

	var (
		err   error
		initp *ContainerProcess
	)

	containerID := "top"
	initp, err = cs.StartContainer(containerID, bundleName)
	t.Assert(err, checker.Equals, nil)

	// Get the shim pid
	b, err := exec.Command("pgrep", "containerd-shim").CombinedOutput()
	t.Assert(err, checker.Equals, nil)
	shimPid := strings.TrimSpace(string(b))
	t.Assert(string(shimPid), checker.Not(checker.Equals), "")

	execID := "top1"
	_, err = cs.AddProcessToContainer(initp, execID, "/", []string{"PATH=/usr/bin"}, []string{"top"}, 0, 0)
	t.Assert(err, checker.Equals, nil)

	for idx, evt := range []types.Event{
		{
			Type:   "start-container",
			Id:     containerID,
			Status: 0,
			Pid:    "",
		},
		{
			Type:   "start-process",
			Id:     containerID,
			Status: 0,
			Pid:    execID,
		},
		{
			Type:   "exit",
			Id:     containerID,
			Status: 0xff,
			Pid:    execID,
		},
	} {
		ch := initp.GetEventsChannel()
		e := <-ch
		evt.Timestamp = e.Timestamp
		t.Assert(*e, checker.Equals, evt)
		if idx == 1 {
			// Process Started, SIGKILL the container shim
			err = exec.Command("kill", "-9", string(shimPid)).Run()
			t.Assert(err, checker.Equals, nil)
		}
	}

	// Container should be dead
	containers, err := cs.ListRunningContainers()
	t.Assert(err, checker.Equals, nil)
	t.Assert(len(containers), checker.Equals, 0)
}
