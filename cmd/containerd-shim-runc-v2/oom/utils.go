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
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// readKVStatsFile is copied from cgroupsv2 package
//
// TODO(fuweid):
//
// we should export some helper functions like MemoryEvents(cgroupPath) directly.
func readKVStatsFile(path string, file string, out map[string]uint64) error {
	f, err := os.Open(filepath.Join(path, file))
	if err != nil {
		return err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		name, value, err := parseKV(s.Text())
		if err != nil {
			return fmt.Errorf("error while parsing %s (line=%q): %w", filepath.Join(path, file), s.Text(), err)
		}
		out[name] = value
	}
	return s.Err()
}

func parseKV(raw string) (string, uint64, error) {
	parts := strings.Fields(raw)
	switch len(parts) {
	case 2:
		v, err := parseUint(parts[1], 10, 64)
		if err != nil {
			return "", 0, err
		}
		return parts[0], v, nil
	default:
		return "", 0, cgroupsv2.ErrInvalidFormat
	}
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
