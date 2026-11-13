/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup1"
	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	"github.com/containerd/log"
	"golang.org/x/sys/unix"
)

const (
	// reapPollInterval is how often a container cgroup is re-read while waiting
	// for the processes killed in it to be reaped.
	reapPollInterval = 10 * time.Millisecond
	// freezePollInterval is how often the state of a cgroup being frozen is
	// re-read.
	freezePollInterval = time.Millisecond
	// reapTimeout bounds the whole cleanup of a container cgroup. This cleanup
	// runs while containerd is starting up, so it must not block for long.
	reapTimeout = 2 * time.Second
	// defaultSlice is the systemd slice a container is placed in when its
	// cgroup path does not name one.
	defaultSlice = "system.slice"
	// cgroup2Mountpoint is where the cgroup v2 hierarchy is mounted.
	cgroup2Mountpoint = "/sys/fs/cgroup"
)

// ociSpecCgroup is a subset of specs.Spec used to reduce garbage during
// unmarshal.
type ociSpecCgroup struct {
	Linux *linuxSpecCgroup
}

// linuxSpecCgroup is a subset of specs.Linux used to reduce garbage during
// unmarshal.
type linuxSpecCgroup struct {
	CgroupsPath string
}

// reapContainerCgroup force kills whatever is still running in the cgroup of
// the container bundled at path, then removes the cgroup. systemd tells whether
// the container was created with the systemd cgroup driver, which determines
// how its cgroup path is to be read.
//
// It is a fallback for when "runc delete --force" fails to tear a container
// down. Processes left behind keep the cgroup populated and hold references to
// the container rootfs, so the rootfs cannot be unmounted and the bundle cannot
// be removed. containerd would then reload that same dead shim and retry the
// same failing cleanup on every subsequent start.
func reapContainerCgroup(ctx context.Context, bundle string, systemd bool) error {
	cgroupsPath, err := readCgroupsPath(bundle)
	if err != nil {
		return err
	}
	if cgroupsPath == "" {
		// No dedicated cgroup was configured for the container.
		return nil
	}
	path := containerCgroupPath(cgroupsPath, systemd)
	if isRootCgroup(path) {
		// The container was left in the root of the hierarchy, which is not a
		// cgroup of its own: it holds every other process on the host.
		return nil
	}
	if !filepath.IsAbs(path) {
		// runc resolves a relative path against the cgroup it was itself run
		// in (on cgroup v2, the parent of that cgroup), which cannot be known
		// for sure here. Rather than risk killing the processes of some other
		// cgroup, leave such a container to "runc delete".
		log.G(ctx).WithField("cgroupsPath", cgroupsPath).Warn("not reaping container cgroup with a relative path")
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, reapTimeout)
	defer cancel()
	if cgroups.Mode() == cgroups.Unified {
		return reapCgroup2(ctx, path)
	}
	return reapCgroup1(ctx, path)
}

// isRootCgroup reports whether path, relative to the cgroup mount point, is the
// root of the hierarchy.
func isRootCgroup(path string) bool {
	return filepath.Join("/", path) == "/"
}

// readCgroupsPath returns the linux.cgroupsPath of the OCI spec in the given
// bundle, or an empty string if the spec declares no cgroup.
func readCgroupsPath(bundle string) (string, error) {
	const configFileName = "config.json"
	b, err := os.ReadFile(filepath.Join(bundle, configFileName))
	if err != nil {
		return "", err
	}
	var spec ociSpecCgroup
	if err := json.Unmarshal(b, &spec); err != nil {
		return "", fmt.Errorf("failed to unmarshal %s: %w", configFileName, err)
	}
	if spec.Linux == nil {
		return "", nil
	}
	return spec.Linux.CgroupsPath, nil
}

// systemdCgroup parses an OCI linux.cgroupsPath in the systemd
// "slice:prefix:name" notation and returns the slice along with the name of the
// scope unit within it.
func systemdCgroup(cgroupsPath string) (slice, unit string, ok bool) {
	parts := strings.Split(cgroupsPath, ":")
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[0], parts[1] + "-" + parts[2] + ".scope", true
}

// expandSlice converts a systemd slice name into its path relative to the
// cgroup mount point, for example "a-b.slice" becomes "a.slice/a-b.slice".
func expandSlice(slice string) string {
	switch slice {
	case "":
		slice = defaultSlice
	case "-.slice":
		// The root slice.
		return ""
	}
	if !strings.HasSuffix(slice, ".slice") || !strings.Contains(slice, "-") {
		return slice
	}
	var path string
	parts := strings.Split(strings.TrimSuffix(slice, ".slice"), "-")
	for i := range parts {
		path = filepath.Join(path, strings.Join(parts[:i+1], "-")+".slice")
	}
	return path
}

// containerCgroupPath converts an OCI linux.cgroupsPath into a path relative to
// the cgroup mount point.
//
// Like runc, it only reads the path in the systemd "slice:prefix:name" notation
// when the systemd cgroup driver is in use: a cgroupfs path may contain colons
// too. The systemd notation is expanded here rather than with
// cgroup2.LoadSystemd, which gets the root slice ("-.slice") wrong, so that
// cgroup v1 and v2 resolve paths the same way.
func containerCgroupPath(cgroupsPath string, systemd bool) string {
	if systemd {
		if slice, unit, ok := systemdCgroup(cgroupsPath); ok {
			return filepath.Join("/", expandSlice(slice), unit)
		}
	}
	return cgroupsPath
}

// cgroupOps are the operations on a cgroup that killCgroup needs, for either
// cgroup version.
type cgroupOps struct {
	// procs lists the processes in the cgroup tree. It returns no processes
	// and no error once the cgroup is gone.
	procs func() ([]int, error)
	// setFrozen requests the cgroup to be frozen or thawed. It is nil when the
	// cgroup cannot be frozen.
	setFrozen func(frozen bool) error
	// frozen reports whether the cgroup is done freezing.
	frozen func() (bool, error)
}

func reapCgroup2(ctx context.Context, path string) error {
	cg, err := cgroupsv2.Load(path)
	if err != nil {
		return err
	}
	dir := filepath.Join(cgroup2Mountpoint, path)
	ops := cgroup2Ops(cg, dir)

	// Most of the time the cgroup is already gone and there is nothing left to
	// do.
	procs, err := ops.procs()
	if err != nil {
		return err
	}
	if len(procs) > 0 {
		log.G(ctx).WithField("pids", procs).Warn("killing processes left behind in the container cgroup")
		if err := killCgroup2(ctx, ops, dir); err != nil {
			return err
		}
	}
	if err := cg.Delete(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// cgroup2Ops returns the operations on the cgroup v2 cgroup cg, whose
// directory is dir.
func cgroup2Ops(cg *cgroupsv2.Manager, dir string) cgroupOps {
	return cgroupOps{
		procs: func() ([]int, error) {
			procs, err := cg.Procs(true)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil, nil
				}
				return nil, err
			}
			pids := make([]int, 0, len(procs))
			for _, p := range procs {
				pids = append(pids, int(p))
			}
			return pids, nil
		},
		setFrozen: func(frozen bool) error {
			v := "0"
			if frozen {
				v = "1"
			}
			return os.WriteFile(filepath.Join(dir, "cgroup.freeze"), []byte(v), 0)
		},
		frozen: func() (bool, error) {
			b, err := os.ReadFile(filepath.Join(dir, "cgroup.events"))
			if err != nil {
				return false, err
			}
			return slices.Contains(strings.Split(string(b), "\n"), "frozen 1"), nil
		},
	}
}

// killCgroup2 kills every process in the cgroup v2 cgroup whose directory is
// dir and waits for them to be gone.
func killCgroup2(ctx context.Context, ops cgroupOps, dir string) error {
	// cgroup.kill kills the whole cgroup tree at once, frozen processes
	// included, and cannot miss processes forked concurrently.
	err := os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0)
	if err == nil {
		return waitCgroupEmpty(ctx, ops)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to kill container cgroup: %w", err)
	}
	// cgroup.kill needs Linux 5.14. cgroup2.Manager.Kill falls back to
	// freezing the cgroup without any bound, so use the same bounded fallback
	// as for cgroup v1 instead.
	return killCgroup(ctx, ops)
}

// waitCgroupEmpty waits for the processes of a killed cgroup to leave it, which
// only happens once they have been reaped by their parent.
func waitCgroupEmpty(ctx context.Context, ops cgroupOps) error {
	for {
		procs, err := ops.procs()
		if err != nil {
			return err
		}
		if len(procs) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %d processes to exit: %w", len(procs), ctx.Err())
		case <-time.After(reapPollInterval):
		}
	}
}

func reapCgroup1(ctx context.Context, path string) error {
	cg, err := cgroup1.Load(cgroup1.StaticPath(path))
	if err != nil {
		if errors.Is(err, cgroup1.ErrCgroupDeleted) {
			return nil
		}
		return err
	}
	ops := cgroup1Ops(cg, path)

	procs, err := ops.procs()
	if err != nil {
		return err
	}
	if len(procs) > 0 {
		log.G(ctx).WithField("pids", procs).Warn("killing processes left behind in the container cgroup")
		if err := killCgroup(ctx, ops); err != nil {
			return err
		}
	}
	return cg.Delete()
}

// cgroup1Ops returns the operations on the cgroup v1 cgroup cg, whose path
// relative to the mount point of each subsystem is path.
func cgroup1Ops(cg cgroup1.Cgroup, path string) cgroupOps {
	subsystem := cgroup1ProcsSubsystem(cg.Subsystems())
	ops := cgroupOps{
		procs: func() ([]int, error) {
			procs, err := cg.Processes(subsystem.Name(), true)
			if err != nil {
				if errors.Is(err, cgroup1.ErrCgroupDeleted) || errors.Is(err, os.ErrNotExist) {
					return nil, nil
				}
				return nil, err
			}
			pids := make([]int, 0, len(procs))
			for _, p := range procs {
				pids = append(pids, p.Pid)
			}
			return pids, nil
		},
	}
	// cgroup1.Cgroup.Freeze waits for the cgroup to freeze without any bound,
	// so drive the freezer directly.
	freezer, ok := subsystem.(interface{ Path(string) string })
	if subsystem.Name() != cgroup1.Freezer || !ok {
		return ops
	}
	state := filepath.Join(freezer.Path(path), "freezer.state")
	ops.setFrozen = func(frozen bool) error {
		v := "THAWED"
		if frozen {
			v = "FROZEN"
		}
		return os.WriteFile(state, []byte(v), 0)
	}
	ops.frozen = func() (bool, error) {
		b, err := os.ReadFile(state)
		if err != nil {
			return false, err
		}
		return strings.TrimSpace(string(b)) == "FROZEN", nil
	}
	return ops
}

// cgroup1ProcsSubsystem picks the subsystem to list the processes of a cgroup
// v1 cgroup from, among its active subsystems, of which there is at least one.
// The freezer is preferred, as it is also used to freeze the cgroup, but any
// active subsystem will do: which ones are available depends on the system, for
// example the devices subsystem is not in a user namespace.
func cgroup1ProcsSubsystem(subsystems []cgroup1.Subsystem) cgroup1.Subsystem {
	for _, s := range subsystems {
		if s.Name() == cgroup1.Freezer {
			return s
		}
	}
	return subsystems[0]
}

// killCgroup kills the processes of a cgroup until none are left, for cgroups
// that cannot be killed with a single operation: all cgroup v1 cgroups, and
// cgroup v2 cgroups on kernels without cgroup.kill. The cgroup is re-read after
// every round to catch processes that were forked in the meantime.
//
// Every round freezes the cgroup, so that its processes cannot fork or exit
// while they are being signalled, and then thaws it again before waiting: a
// frozen task does not act on SIGKILL until it is thawed. Thawing also matters
// when the cgroup was left frozen to begin with, for example by a "runc create"
// that was killed while it was setting the cgroup up.
func killCgroup(ctx context.Context, ops cgroupOps) error {
	for {
		frozen := freezeCgroup(ctx, ops)
		procs, err := ops.procs()
		if err == nil && len(procs) > 0 {
			err = killCgroupProcs(ctx, ops, procs, frozen)
		}
		if ops.setFrozen != nil {
			if err := ops.setFrozen(false); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.G(ctx).WithError(err).Warn("failed to thaw container cgroup")
			}
		}
		if err != nil {
			return err
		}
		if len(procs) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %d processes to exit: %w", len(procs), ctx.Err())
		case <-time.After(reapPollInterval):
		}
	}
}

// freezeCgroup freezes a cgroup, giving up when ctx is done, and reports
// whether the cgroup got frozen.
func freezeCgroup(ctx context.Context, ops cgroupOps) bool {
	if ops.setFrozen == nil {
		return false
	}
	for {
		// The request is repeated on every attempt, as a cgroup v1 freezer
		// can go back to thawed when freezing some task fails.
		err := ops.setFrozen(true)
		var frozen bool
		if err == nil {
			frozen, err = ops.frozen()
		}
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.G(ctx).WithError(err).Warn("failed to freeze container cgroup")
			}
			return false
		}
		if frozen {
			return true
		}
		select {
		case <-ctx.Done():
			log.G(ctx).Warn("timed out freezing container cgroup")
			return false
		case <-time.After(freezePollInterval):
		}
	}
}

// killCgroupProcs sends SIGKILL to procs, which were listed as members of a
// cgroup, without ever signalling an unrelated process that was given the PID
// of one of them after it exited. frozen tells whether the cgroup was frozen
// before procs were listed.
func killCgroupProcs(ctx context.Context, ops cgroupOps, procs []int, frozen bool) error {
	pidfds := make(map[int]int, len(procs))
	defer func() {
		for _, fd := range pidfds {
			unix.Close(fd)
		}
	}()
	for _, pid := range procs {
		fd, err := unix.PidfdOpen(pid, 0)
		switch {
		case err == nil:
			pidfds[pid] = fd
		case errors.Is(err, unix.ESRCH):
			// Already gone.
		case errors.Is(err, unix.ENOSYS):
			// pidfds need Linux 5.3.
			return killFrozenCgroupProcs(ctx, procs, frozen)
		default:
			return fmt.Errorf("failed to open pidfd of process %d: %w", pid, err)
		}
	}
	if len(pidfds) == 0 {
		return nil
	}

	// A PID that is still in the cgroup once its pidfd is open belongs to the
	// process the pidfd refers to, unless that process has exited and its PID
	// has been reused since, in which case signalling the pidfd fails with
	// ESRCH. Either way, only members of the cgroup get signalled.
	members, err := ops.procs()
	if err != nil {
		return err
	}
	for _, pid := range members {
		fd, ok := pidfds[pid]
		if !ok {
			continue
		}
		if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
			log.G(ctx).WithError(err).WithField("pid", pid).Warn("failed to kill process in the container cgroup")
		}
	}
	return nil
}

// killFrozenCgroupProcs sends SIGKILL to procs by PID, which is only safe when
// they were listed while their cgroup was frozen: the processes of a frozen
// cgroup cannot exit, so their PIDs cannot be reused until it is thawed.
func killFrozenCgroupProcs(ctx context.Context, procs []int, frozen bool) error {
	if !frozen {
		return errors.New("cannot safely kill processes in a cgroup that cannot be frozen without pidfd support")
	}
	for _, pid := range procs {
		if err := unix.Kill(pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
			log.G(ctx).WithError(err).WithField("pid", pid).Warn("failed to kill process in the container cgroup")
		}
	}
	return nil
}
