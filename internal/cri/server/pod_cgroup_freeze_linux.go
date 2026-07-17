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

package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup1"
)

const podCgroupTransitionPollInterval = 10 * time.Millisecond

type podCgroupState int

const (
	podCgroupStateUnknown podCgroupState = iota
	podCgroupStateThawed
	podCgroupStateTransitioning
	podCgroupStateFrozen
)

func (s podCgroupState) String() string {
	switch s {
	case podCgroupStateThawed:
		return "thawed"
	case podCgroupStateTransitioning:
		return "transitioning"
	case podCgroupStateFrozen:
		return "frozen"
	default:
		return "unknown"
	}
}

type podCgroupFreezer interface {
	State(context.Context) (podCgroupState, error)
	Freeze(context.Context) error
	Thaw(context.Context) error
}

type directPodCgroupFreezer struct {
	controlPath string
	statePath   string
	version     int
}

func loadPodCgroupFreezer(cgroupPath string) (podCgroupFreezer, error) {
	if err := validatePodCgroupPath(cgroupPath); err != nil {
		return nil, err
	}

	switch mode := cgroups.Mode(); mode {
	case cgroups.Unified:
		root := filepath.Join("/sys/fs/cgroup", cgroupPath)
		return &directPodCgroupFreezer{
			controlPath: filepath.Join(root, "cgroup.freeze"),
			statePath:   filepath.Join(root, "cgroup.events"),
			version:     2,
		}, nil
	case cgroups.Legacy, cgroups.Hybrid:
		subsystems, err := cgroup1.Default()
		if err != nil {
			return nil, fmt.Errorf("failed to discover the cgroup v1 freezer hierarchy: %w", err)
		}
		for _, subsystem := range subsystems {
			if subsystem.Name() != cgroup1.Freezer {
				continue
			}
			pather, ok := subsystem.(interface{ Path(string) string })
			if !ok {
				return nil, errors.New("cgroup v1 freezer hierarchy does not expose its filesystem path")
			}
			statePath := filepath.Join(pather.Path(cgroupPath), "freezer.state")
			return &directPodCgroupFreezer{controlPath: statePath, statePath: statePath, version: 1}, nil
		}
		return nil, errors.New("pod checkpoint requires the cgroup v1 freezer controller")
	case cgroups.Unavailable:
		return nil, errors.New("pod checkpoint requires cgroups, but no cgroup filesystem is available")
	default:
		return nil, fmt.Errorf("pod checkpoint does not support cgroup mode %d", mode)
	}
}

func validatePodCgroupPath(cgroupPath string) error {
	if cgroupPath == "" {
		return errors.New("pod checkpoint requires a pod cgroup parent, but the sandbox config has none")
	}
	if !filepath.IsAbs(cgroupPath) {
		return fmt.Errorf("pod cgroup parent %q must be an absolute path", cgroupPath)
	}
	if filepath.Clean(cgroupPath) != cgroupPath {
		return fmt.Errorf("pod cgroup parent %q must be a clean path", cgroupPath)
	}
	if cgroupPath == string(filepath.Separator) {
		return errors.New("refusing to freeze the root cgroup for a pod checkpoint")
	}
	const cgroupMountpoint = "/sys/fs/cgroup"
	if cgroupPath == cgroupMountpoint || strings.HasPrefix(cgroupPath, cgroupMountpoint+string(filepath.Separator)) {
		return fmt.Errorf("pod cgroup parent %q must be relative to the cgroup filesystem mountpoint", cgroupPath)
	}
	return nil
}

func (f *directPodCgroupFreezer) State(ctx context.Context) (podCgroupState, error) {
	if err := ctx.Err(); err != nil {
		return podCgroupStateUnknown, err
	}
	switch f.version {
	case 1:
		data, err := os.ReadFile(f.statePath)
		if err != nil {
			return podCgroupStateUnknown, fmt.Errorf("failed to read cgroup v1 freezer state %q: %w", f.statePath, err)
		}
		switch strings.TrimSpace(string(data)) {
		case "THAWED":
			return podCgroupStateThawed, nil
		case "FREEZING":
			return podCgroupStateTransitioning, nil
		case "FROZEN":
			return podCgroupStateFrozen, nil
		default:
			return podCgroupStateUnknown, fmt.Errorf("cgroup v1 freezer %q returned invalid state %q", f.statePath, strings.TrimSpace(string(data)))
		}
	case 2:
		control, err := os.ReadFile(f.controlPath)
		if err != nil {
			return podCgroupStateUnknown, fmt.Errorf("failed to read cgroup v2 freeze control %q: %w", f.controlPath, err)
		}
		controlState := strings.TrimSpace(string(control))
		if controlState != "0" && controlState != "1" {
			return podCgroupStateUnknown, fmt.Errorf("cgroup v2 freeze control %q returned invalid state %q", f.controlPath, controlState)
		}
		events, err := os.Open(f.statePath)
		if err != nil {
			return podCgroupStateUnknown, fmt.Errorf("failed to read cgroup v2 events %q: %w", f.statePath, err)
		}
		defer events.Close()
		frozenState := ""
		scanner := bufio.NewScanner(events)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) == 2 && fields[0] == "frozen" {
				frozenState = fields[1]
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return podCgroupStateUnknown, fmt.Errorf("failed to parse cgroup v2 events %q: %w", f.statePath, err)
		}
		switch {
		case controlState == "0" && frozenState == "0":
			return podCgroupStateThawed, nil
		case controlState == "1" && frozenState == "1":
			return podCgroupStateFrozen, nil
		case (controlState == "0" || controlState == "1") && (frozenState == "0" || frozenState == "1"):
			return podCgroupStateTransitioning, nil
		default:
			return podCgroupStateUnknown, fmt.Errorf("cgroup v2 events %q has no valid frozen field", f.statePath)
		}
	default:
		return podCgroupStateUnknown, fmt.Errorf("unsupported cgroup freezer version %d", f.version)
	}
}

func (f *directPodCgroupFreezer) Freeze(ctx context.Context) error {
	state, err := f.State(ctx)
	if err != nil {
		return err
	}
	if state != podCgroupStateThawed {
		return fmt.Errorf("refusing to checkpoint a pod whose cgroup is %s", state)
	}
	return f.transition(ctx, podCgroupStateFrozen)
}

func (f *directPodCgroupFreezer) Thaw(ctx context.Context) error {
	return f.transition(ctx, podCgroupStateThawed)
}

func (f *directPodCgroupFreezer) transition(ctx context.Context, desired podCgroupState) error {
	var value string
	switch {
	case f.version == 1 && desired == podCgroupStateFrozen:
		value = "FROZEN"
	case f.version == 1 && desired == podCgroupStateThawed:
		value = "THAWED"
	case f.version == 2 && desired == podCgroupStateFrozen:
		value = "1"
	case f.version == 2 && desired == podCgroupStateThawed:
		value = "0"
	default:
		return fmt.Errorf("unsupported cgroup freezer transition: version=%d state=%s", f.version, desired)
	}

	ticker := time.NewTicker(podCgroupTransitionPollInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for pod cgroup to become %s: %w", desired, err)
		}
		if err := os.WriteFile(f.controlPath, []byte(value), 0o600); err != nil {
			return fmt.Errorf("failed to request pod cgroup state %s via %q: %w", desired, f.controlPath, err)
		}
		state, err := f.State(ctx)
		if err != nil {
			return err
		}
		if state == desired {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for pod cgroup to become %s: %w", desired, ctx.Err())
		case <-ticker.C:
		}
	}
}
