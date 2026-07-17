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

package podsandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
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

type directPodCgroupFreezer struct {
	controlPath string
	statePath   string
	version     int
}

// podCgroupFilesystemPath returns the path of the Pod cgroup relative to the
// cgroup filesystem mountpoint.
func podCgroupFilesystemPath(cgroupParent string) (string, error) {
	if err := validatePodCgroupPath(cgroupParent); err != nil {
		return "", err
	}
	if slice := path.Base(cgroupParent); strings.HasSuffix(slice, ".slice") {
		return expandSystemdSlice(slice)
	}
	return cgroupParent, nil
}

func validatePodCgroupPath(cgroupPath string) error {
	if cgroupPath == "" {
		return errors.New("pod checkpoint requires a pod cgroup parent, but the sandbox config has none")
	}
	// Like getCgroupsPath, treat a parent ending in a slice as a systemd
	// slice. runc's systemd driver uses only the last slice name.
	if slice := path.Base(cgroupPath); strings.HasSuffix(slice, ".slice") {
		_, err := expandSystemdSlice(slice)
		return err
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

// expandSystemdSlice returns the cgroup filesystem path of a systemd slice.
// Like runc's systemd driver, it nests the slice under the slices named by its
// dash-separated prefixes: "a-b.slice" is "/a.slice/a-b.slice".
func expandSystemdSlice(slice string) (string, error) {
	const suffix = ".slice"
	name := strings.TrimSuffix(slice, suffix)
	if name == slice || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("invalid systemd slice name %q for the pod cgroup parent", slice)
	}
	if name == "-" {
		return "", errors.New("refusing to freeze the root systemd slice for a pod checkpoint")
	}

	var expanded strings.Builder
	var prefix string
	for component := range strings.SplitSeq(name, "-") {
		if component == "" {
			return "", fmt.Errorf("invalid systemd slice name %q for the pod cgroup parent", slice)
		}
		expanded.WriteByte('/')
		expanded.WriteString(prefix)
		expanded.WriteString(component)
		expanded.WriteString(suffix)
		prefix += component + "-"
	}
	return expanded.String(), nil
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

func (f *directPodCgroupFreezer) Freeze(ctx context.Context) (bool, error) {
	state, err := f.State(ctx)
	if err != nil {
		return false, err
	}
	if state != podCgroupStateThawed {
		return false, fmt.Errorf("refusing to checkpoint a pod whose cgroup is %s", state)
	}
	return f.transition(ctx, podCgroupStateFrozen)
}

func (f *directPodCgroupFreezer) Thaw(ctx context.Context) error {
	_, err := f.transition(ctx, podCgroupStateThawed)
	return err
}

func (f *directPodCgroupFreezer) transition(ctx context.Context, desired podCgroupState) (bool, error) {
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
		return false, fmt.Errorf("unsupported cgroup freezer transition: version=%d state=%s", f.version, desired)
	}

	ticker := time.NewTicker(podCgroupTransitionPollInterval)
	defer ticker.Stop()
	requested := false
	for {
		if err := ctx.Err(); err != nil {
			return requested, fmt.Errorf("waiting for pod cgroup to become %s: %w", desired, err)
		}
		requested = true
		if err := os.WriteFile(f.controlPath, []byte(value), 0o600); err != nil {
			return requested, fmt.Errorf("failed to request pod cgroup state %s via %q: %w", desired, f.controlPath, err)
		}
		state, err := f.State(ctx)
		if err != nil {
			return requested, err
		}
		if state == desired {
			return requested, nil
		}
		select {
		case <-ctx.Done():
			return requested, fmt.Errorf("waiting for pod cgroup to become %s: %w", desired, ctx.Err())
		case <-ticker.C:
		}
	}
}
