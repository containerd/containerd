// Copyright 2020-2021 Intel Corporation. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cgroups

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"syscall"
)

// Controller is our enumerated type for cgroup controllers.
type Controller int

// Group represents a control group.
type Group string

//nolint
const (
	// UnkownController represents a controller of unknown type.
	UnknownController Controller = iota
	// blkio cgroup controller.
	Blkio
	// cpu cgroup controller.
	Cpu
	// cpuacct cgroup controller.
	Cpuacct
	// cpuset cgroup controller.
	Cpuset
	// devices cgroup controller.
	Devices
	// freezer cgroup controller.
	Freezer
	// hugetlb cgroup controller.
	Hugetlb
	// memory cgroup controller.
	Memory
	// net_cls cgroup controller.
	NetCls
	// net_prio cgroup controller.
	NetPrio
	// per_event cgroup controller.
	PerfEvent
	// pids cgroup controller.
	Pids
)

var (
	// controllerNames maps controllers to names/relative paths.
	controllerNames = map[Controller]string{
		Blkio:     "blkio",
		Cpu:       "cpu",
		Cpuacct:   "cpuacct",
		Cpuset:    "cpuset",
		Devices:   "devices",
		Freezer:   "freezer",
		Hugetlb:   "hugetlb",
		Memory:    "memory",
		NetCls:    "net_cls",
		NetPrio:   "net_prio",
		PerfEvent: "perf_event",
		Pids:      "pids",
	}

	// controllerNames maps controllers to names/relative paths.
	controllerDirs = map[string]Controller{
		"blkio":      Blkio,
		"cpu":        Cpu,
		"cpuacct":    Cpuacct,
		"cpuset":     Cpuset,
		"devices":    Devices,
		"freezer":    Freezer,
		"hugetlb":    Hugetlb,
		"memory":     Memory,
		"net_cls":    NetCls,
		"net_prio":   NetPrio,
		"perf_event": PerfEvent,
		"pids":       Pids,
	}
)

// String returns the name of the given controller.
func (c Controller) String() string {
	if name, ok := controllerNames[c]; ok {
		return name
	}
	return "unknown"
}

// Path returns the absolute path of the given controller.
func (c Controller) Path() string {
	return path.Join(mountDir, c.String())
}

// RelPath returns the relative path of the given controller.
func (c Controller) RelPath() string {
	return c.String()
}

// Group returns the given group for the controller.
func (c Controller) Group(group string) Group {
	return Group(path.Join(mountDir, c.String(), group))
}

// AsGroup returns the group for the given absolute directory path.
func AsGroup(absDir string) Group {
	return Group(absDir)
}

// Controller returns the controller for the group.
func (g Group) Controller() Controller {
	relPath := strings.TrimPrefix(string(g), mountDir+"/")
	split := strings.SplitN(relPath, "/", 2)
	if len(split) > 0 {
		return controllerDirs[split[0]]
	}
	return UnknownController
}

// GetTasks reads the pids of threads currently assigned to the group.
func (g Group) GetTasks() ([]string, error) {
	return g.readPids(Tasks)
}

// GetProcesses reads the pids of processes currently assigned to the group.
func (g Group) GetProcesses() ([]string, error) {
	return g.readPids(Procs)
}

// AddTasks writes the given thread pids to the group.
func (g Group) AddTasks(pids ...string) error {
	return g.writePids(Tasks, pids...)
}

// AddProcesses writes the given process pids to the group.
func (g Group) AddProcesses(pids ...string) error {
	return g.writePids(Procs, pids...)
}

// Write writes the formatted data to the groups entry.
func (g Group) Write(entry, format string, args ...interface{}) error {
	entryPath := path.Join(string(g), entry)
	f, err := fsi.OpenFile(entryPath, os.O_WRONLY, 0644)
	if err != nil {
		return g.errorf("%q: failed to open for writing: %v", entry, err)
	}
	defer f.Close()

	data := fmt.Sprintf(format, args...)
	if _, err := f.Write([]byte(data)); err != nil {
		return g.errorf("%q: failed to write %q: %v", entry, data, err)
	}

	return nil
}

// Read the groups entry and return contents in a string
func (g Group) Read(entry string) (string, error) {
	var buf bytes.Buffer
	entryPath := path.Join(string(g), entry)
	f, err := fsi.OpenFile(entryPath, os.O_RDONLY, 0644)
	if err != nil {
		return "", g.errorf("%q: failed to open for reading: %v", entry, err)
	}
	defer f.Close()
	if _, err := buf.ReadFrom(f); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// readPids reads pids from a cgroup's tasks or procs entry.
func (g Group) readPids(entry string) ([]string, error) {
	var pids []string

	pidFile := path.Join(string(g), entry)

	f, err := fsi.OpenFile(pidFile, os.O_RDONLY, 0644)
	if err != nil {
		return nil, g.errorf("failed to open %q: %v", entry, err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		pids = append(pids, s.Text())
	}
	if s.Err() != nil {
		return nil, g.errorf("failed to read %q: %v", entry, err)
	}

	return pids, nil
}

// writePids writes pids to a cgroup's tasks or procs entry.
func (g Group) writePids(entry string, pids ...string) error {
	pidFile := path.Join(string(g), entry)

	f, err := fsi.OpenFile(pidFile, os.O_WRONLY, 0644)
	if err != nil {
		return g.errorf("failed to write pids to %q: %v", pidFile, err)
	}
	defer f.Close()

	for _, pid := range pids {
		if _, err := f.Write([]byte(pid)); err != nil {
			if !errors.Is(err, syscall.ESRCH) {
				return g.errorf("failed to write pid %s to %q: %v",
					pidFile, pid, err)
			}
		}
	}

	return nil
}

// error returns a formatted group-specific error.
func (g Group) errorf(format string, args ...interface{}) error {
	name := strings.TrimPrefix(string(g), mountDir+"/")
	return fmt.Errorf("cgroup "+name+": "+format, args...)
}
