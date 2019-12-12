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

package v2

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/cgroups/v2/stats"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	cgroupProcs    = "cgroup.procs"
	defaultDirPerm = 0755
)

// defaultFilePerm is a var so that the test framework can change the filemode
// of all files created when the tests are running.  The difference between the
// tests and real world use is that files like "cgroup.procs" will exist when writing
// to a read cgroup filesystem and do not exist prior when running in the tests.
// this is set to a non 0 value in the test code
var defaultFilePerm = os.FileMode(0)

// remove will remove a cgroup path handling EAGAIN and EBUSY errors and
// retrying the remove after a exp timeout
func remove(path string) error {
	var err error
	delay := 10 * time.Millisecond
	for i := 0; i < 5; i++ {
		if i != 0 {
			time.Sleep(delay)
			delay *= 2
		}
		if err = os.RemoveAll(path); err == nil {
			return nil
		}
	}
	return errors.Wrapf(err, "cgroups: unable to remove path %q", path)
}

// parseCgroupProcsFile parses /sys/fs/cgroup/$GROUPPATH/cgroup.procs
func parseCgroupProcsFile(path string) ([]uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var (
		out []uint64
		s   = bufio.NewScanner(f)
	)
	for s.Scan() {
		if t := s.Text(); t != "" {
			pid, err := strconv.ParseUint(t, 10, 0)
			if err != nil {
				return nil, err
			}
			out = append(out, pid)
		}
	}
	return out, nil
}

func parseKV(raw string) (string, interface{}, error) {
	parts := strings.Fields(raw)
	switch len(parts) {
	case 2:
		v, err := parseUint(parts[1], 10, 64)
		if err != nil {
			// if we cannot parse as a uint, parse as a string
			return parts[0], parts[1], nil
		}
		return parts[0], v, nil
	default:
		return "", 0, ErrInvalidFormat
	}
}

func readUint(path string) (uint64, error) {
	v, err := ioutil.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return parseUint(strings.TrimSpace(string(v)), 10, 64)
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

// parseCgroupFile parses /proc/PID/cgroup file and return string
func parseCgroupFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return parseCgroupFromReader(f)
}

func parseCgroupFromReader(r io.Reader) (string, error) {
	var (
		s = bufio.NewScanner(r)
	)
	for s.Scan() {
		if err := s.Err(); err != nil {
			return "", err
		}
		var (
			text  = s.Text()
			parts = strings.SplitN(text, ":", 3)
		)
		if len(parts) < 3 {
			return "", fmt.Errorf("invalid cgroup entry: %q", text)
		}
		// text is like "0::/user.slice/user-1001.slice/session-1.scope"
		if parts[0] == "0" && parts[1] == "" {
			return parts[2], nil
		}
	}
	return "", fmt.Errorf("cgroup path not found")
}

// ToResources converts the oci LinuxResources struct into a
// v2 Resources type for use with this package.
//
// converting cgroups configuration from v1 to v2
// ref: https://github.com/containers/crun/blob/master/crun.1.md#cgroup-v2
func ToResources(spec *specs.LinuxResources) *Resources {
	var resources Resources
	if cpu := spec.CPU; cpu != nil {
		resources.CPU = &CPU{
			Cpus: cpu.Cpus,
			Mems: cpu.Mems,
		}
		if shares := cpu.Shares; shares != nil {
			convertedWeight := (1 + ((*shares-2)*9999)/262142)
			resources.CPU.Weight = &convertedWeight
		}
		if period := cpu.Period; period != nil {
			resources.CPU.Max = period
		}
	}
	if mem := spec.Memory; mem != nil {
		resources.Memory = &Memory{}
		if swap := mem.Swap; swap != nil {
			resources.Memory.Swap = swap
		}
		if l := mem.Limit; l != nil {
			resources.Memory.Max = l
		}
		if l := mem.Reservation; l != nil {
			resources.Memory.Low = l
		}
	}
	if pids := spec.Pids; pids != nil {
		resources.Pids = &Pids{
			Max: pids.Limit,
		}
	}
	if i := spec.BlockIO; i != nil {
		resources.IO = &IO{}
		if i.Weight != nil {
			resources.IO.BFQ.Weight = *i.Weight
		}
		for t, devices := range map[IOType][]specs.LinuxThrottleDevice{
			ReadBPS:   i.ThrottleReadBpsDevice,
			WriteBPS:  i.ThrottleWriteBpsDevice,
			ReadIOPS:  i.ThrottleReadIOPSDevice,
			WriteIOPS: i.ThrottleWriteIOPSDevice,
		} {
			for _, d := range devices {
				resources.IO.Max = append(resources.IO.Max, Entry{
					Type:  t,
					Major: d.Major,
					Minor: d.Minor,
					Rate:  d.Rate,
				})
			}
		}
	}
	if i := spec.Rdma; i != nil {
		resources.RDMA = &RDMA{}
		for device, value := range spec.Rdma {
			if device != "" && (value.HcaHandles != nil || value.HcaObjects != nil) {
				resources.RDMA.Limit = append(resources.RDMA.Limit, RDMAEntry{
					Device:     device,
					HcaHandles: *value.HcaHandles,
					HcaObjects: *value.HcaObjects,
				})
			}
		}
	}

	return &resources
}

// Gets uint64 parsed content of single value cgroup stat file
func getStatFileContentUint64(filePath string) uint64 {
	contents, err := ioutil.ReadFile(filePath)
	if err != nil {
		logrus.Error(err)
		return 0
	}
	trimmed := strings.TrimSpace(string(contents))
	if trimmed == "max" {
		return math.MaxUint64
	}

	res, err := parseUint(trimmed, 10, 64)
	if err != nil {
		logrus.Errorf("unable to parse %q as a uint from Cgroup file %q", string(contents), filePath)
		return res
	}

	return res
}
func rdmaStats(filepath string) []*stats.RdmaEntry {
	currentData, err := ioutil.ReadFile(filepath)
	if err != nil {
		return []*stats.RdmaEntry{}
	}
	return toRdmaEntry(strings.Split(string(currentData), "\n"))
}

func parseRdmaKV(raw string, entry *stats.RdmaEntry) {
	var value uint64
	var err error

	parts := strings.Split(raw, "=")
	switch len(parts) {
	case 2:
		if parts[1] == "max" {
			value = math.MaxUint32
		} else {
			value, err = parseUint(parts[1], 10, 32)
			if err != nil {
				return
			}
		}
		if parts[0] == "hca_handle" {
			entry.HcaHandles = uint32(value)
		} else if parts[0] == "hca_object" {
			entry.HcaObjects = uint32(value)
		}
	}
}

func toRdmaEntry(strEntries []string) []*stats.RdmaEntry {
	var rdmaEntries []*stats.RdmaEntry
	for i := range strEntries {
		parts := strings.Fields(strEntries[i])
		switch len(parts) {
		case 3:
			entry := new(stats.RdmaEntry)
			entry.Device = parts[0]
			parseRdmaKV(parts[1], entry)
			parseRdmaKV(parts[2], entry)

			rdmaEntries = append(rdmaEntries, entry)
		default:
			continue
		}
	}
	return rdmaEntries
}
