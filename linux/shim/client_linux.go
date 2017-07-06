// +build linux

package shim

import (
	"context"
	"os/exec"
	"syscall"

	"github.com/containerd/cgroups"
	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var atter = syscall.SysProcAttr{
	Cloneflags: syscall.CLONE_NEWNS,
	Setpgid:    true,
}

func setCgroup(ctx context.Context, config Config, cmd *exec.Cmd) error {
	cg, err := cgroups.Load(cgroups.V1, cgroups.StaticPath(config.CgroupPath))
	if err != nil {
		return errors.Wrapf(err, "failed to load cgroup %s", config.CgroupPath)
	}
	if err := cg.Add(cgroups.Process{
		Pid: cmd.Process.Pid,
	}); err != nil {
		return errors.Wrapf(err, "failed to join cgroup %s", config.CgroupPath)
	}
	log.G(ctx).WithFields(logrus.Fields{
		"pid":     cmd.Process.Pid,
		"address": config.Address,
	}).Infof("shim placed in cgroup %s", config.CgroupPath)
	return nil
}
