package netns

import (
	"os"
	"runtime"
	"sync"

	cnins "github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

const nsRunDir = "/var/run/netns"

// RecoverNetNS recreate a persistent network namespace if the ns is not exists.
// Otherwise, do nothing.
func RecoverNetNS(nsPath string) error {
	if nsPath == "" {
		return errors.Errorf("cannot pass empty netns path")
	}
	_, err := cnins.GetNS(nsPath)
	if err == nil {
		// net ns already exists
		return nil
	}
	// Recreate the directory for mounting network namespaces just
	// in case that the system reboots
	if err := os.MkdirAll(nsRunDir, 0755); err != nil {
		return err
	}
	mountPointFd, err := os.Create(nsPath)
	if err != nil {
		return err
	}
	mountPointFd.Close()
	defer func() {
		// Ensure the mount point is cleaned up on errors
		if err != nil {
			os.RemoveAll(nsPath) // nolint: errcheck
		}
	}()
	var wg sync.WaitGroup
	wg.Add(1)
	// do namespace work in a dedicated goroutine, so that we can safely
	// Lock/Unlock OSThread without upsetting the lock/unlock state of
	// the caller of this function
	go (func() {
		defer wg.Done()
		runtime.LockOSThread()
		// Don't unlock. By not unlocking, golang will kill the OS thread when the
		// goroutine is done (for go1.10+)
		var origNS cnins.NetNS
		origNS, err = cnins.GetNS(getCurrentThreadNetNSPath())
		if err != nil {
			return
		}
		defer origNS.Close()
		// create a new netns on the current thread
		err = unix.Unshare(unix.CLONE_NEWNET)
		if err != nil {
			return
		}
		// Put this thread back to the orig ns, since it might get reused (pre go1.10)
		defer origNS.Set() // nolint: errcheck
		// bind mount the netns from the current thread (from /proc) onto the
		// mount point. This causes the namespace to persist, even when there
		// are no threads in the ns.
		err = unix.Mount(getCurrentThreadNetNSPath(), nsPath, "none", unix.MS_BIND, "")
		if err != nil {
			err = errors.Wrapf(err, "failed to bind mount ns at %s", nsPath)
		}
	})()
	wg.Wait()
	return errors.Wrapf(err, "failed to create namespace on %s", nsPath)
}
