//go:build linux

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

package oom

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	"golang.org/x/sys/unix"
)

func memoryEventNonBlockFD(cgroupPath string) (_ *os.File, retErr error) {
	rawFd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("failed to create inotify fd: %w", err)
	}

	fd := os.NewFile(uintptr(rawFd), "inotifyfd")
	defer func() {
		if retErr != nil {
			fd.Close()
		}
	}()

	fpath := filepath.Join(cgroupPath, "memory.events")
	if _, err := unix.InotifyAddWatch(rawFd, fpath, unix.IN_MODIFY); err != nil {
		return nil, fmt.Errorf("failed to add inotify watch for %q: %w", fpath, err)
	}

	// monitor to detect process exit/cgroup deletion
	evpath := filepath.Join(cgroupPath, "cgroup.events")
	if _, err = unix.InotifyAddWatch(rawFd, evpath, unix.IN_MODIFY); err != nil {
		return nil, fmt.Errorf("failed to add inotify watch for %q: %w", evpath, err)
	}
	return fd, nil
}

// getCgroup2Path should be removed if cgroups package can support GetPath method.
func getCgroup2Path(pid int) (string, error) {
	defaultCgroup2Path := "/sys/fs/cgroup"

	g, err := cgroupsv2.PidGroupPath(pid)
	if err != nil {
		return "", fmt.Errorf("failed to load cgroup2 path from pid: %w", err)
	}

	if err := cgroupsv2.VerifyGroupPath(g); err != nil {
		return "", fmt.Errorf("invalid: cgroup2 path (%s): %w", g, err)
	}
	return filepath.Join(defaultCgroup2Path, g), nil
}

// memoryEventsBufSize is large enough to hold the whole cgroup v2
// memory.events file, which is a small, fixed set of counters.
const memoryEventsBufSize = 512

// oomKillPrefix is the start of the "oom_kill" line in memory.events. Keeping
// it as a package-level []byte avoids a per-call allocation on the OOM watcher
// hot path.
var oomKillPrefix = []byte("oom_kill ")

// readMemoryOOMKill reads the "oom_kill" counter from a cgroup's memory.events
// file into the caller-provided buffer.
//
// The OOM watcher is woken for every modification of memory.events, which the
// kernel updates on every low/high/max reclaim event, not only on OOM kills.
// On long-running nodes with stable workloads those reclaim events dominate, so
// this is a hot path. Reading the file into a reused buffer and scanning for the
// single counter we care about avoids allocating a map (and re-parsing every
// line) on each wakeup, which otherwise accumulates measurable CPU and GC
// overhead over the lifetime of the shim.
// See https://github.com/containerd/containerd/issues/13558.
func readMemoryOOMKill(cgroupPath string, buf []byte) (uint64, error) {
	f, err := os.Open(filepath.Join(cgroupPath, "memory.events"))
	if err != nil {
		return 0, err
	}
	defer f.Close()

	n, err := io.ReadFull(f, buf)
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		// The whole file fit within buf; this is the expected path for current
		// kernels, where memory.events is a small, fixed set of counters.
	case err != nil:
		return 0, err
	default:
		// buf was filled completely, so memory.events may be larger than buf
		// and the oom_kill counter could lie beyond what we read. Read the
		// remainder so a truncated buffer can never cause a missed OOM event.
		// This is not expected for current kernels and keeps the common path
		// allocation-free.
		rest, rerr := io.ReadAll(f)
		if rerr != nil {
			return 0, rerr
		}
		full := make([]byte, 0, n+len(rest))
		full = append(append(full, buf[:n]...), rest...)
		v, _ := parseOOMKill(full)
		return v, nil
	}

	v, _ := parseOOMKill(buf[:n])
	return v, nil
}

// parseOOMKill scans memory.events content for the "oom_kill" counter. The
// boolean return reports whether the counter was found; a missing counter is
// reported as (0, false) so the caller can distinguish it from oom_kill being
// genuinely zero.
func parseOOMKill(data []byte) (uint64, bool) {
	for len(data) > 0 {
		line := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		rest, ok := bytes.CutPrefix(line, oomKillPrefix)
		if !ok {
			continue
		}
		v, err := parseUint(string(bytes.TrimSpace(rest)), 10, 64)
		if err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

func parseUint(s string, base, bitSize int) (uint64, error) {
	v, err := strconv.ParseUint(s, base, bitSize)
	if err != nil {
		intValue, intErr := strconv.ParseInt(s, base, bitSize)
		// 1. Handle negative values greater than MinInt64 (and)
		// 2. Handle negative values lesser than MinInt64
		if intErr == nil && intValue < 0 {
			return 0, nil
		} else if intErr != nil &&
			intErr.(*strconv.NumError).Err == strconv.ErrRange &&
			intValue < 0 {
			return 0, nil
		}
		return 0, err
	}
	return v, nil
}
