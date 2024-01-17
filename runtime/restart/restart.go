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

// Package restart enables containers to have labels added and monitored to
// keep the container's task running if it is killed.
//
// Setting the StatusLabel on a container instructs the restart monitor to keep
// that container's task in a specific status.
// Setting the LogPathLabel on a container will setup the task's IO to be redirected
// to a log file when running a task within the restart manager.
//
// The restart labels can be cleared off of a container using the WithNoRestarts Opt.
//
// The restart monitor has one option in the containerd config under the [plugins.restart]
// section.  `interval = "10s" sets the reconcile interval that the restart monitor checks
// for task state and reconciles the desired status for that task.
package restart

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/log"
)

const (
	// StatusLabel sets the restart status label for a container
	StatusLabel = "containerd.io/restart.status"
	// LogURILabel sets the restart log uri label for a container
	LogURILabel = "containerd.io/restart.loguri"

	// PolicyLabel sets the restart policy label for a container
	PolicyLabel = "containerd.io/restart.policy"
	// CountLabel sets the restart count label for a container
	CountLabel = "containerd.io/restart.count"
	// ExplicitlyStoppedLabel sets the restart explicitly stopped label for a container
	ExplicitlyStoppedLabel = "containerd.io/restart.explicitly-stopped"
)

// Policy represents the restart policies of a container.
type Policy struct {
	name              string
	maximumRetryCount int
}

// NewPolicy creates a restart policy with the specified name.
// supports the following restart policies:
// - no, Do not restart the container.
// - always, Always restart the container regardless of the exit status.
// - on-failure[:max-retries], Restart only if the container exits with a non-zero exit status.
// - unless-stopped, Always restart the container unless it is stopped.
func NewPolicy(policy string) (*Policy, error) {
	policySlice := strings.Split(policy, ":")
	var (
		err        error
		retryCount int
	)
	switch policySlice[0] {
	case "", "no", "always", "unless-stopped":
		policy = policySlice[0]
		if policy == "" {
			policy = "always"
		}
		if len(policySlice) > 1 {
			return nil, fmt.Errorf("restart policy %q not support max retry count", policySlice[0])
		}
	case "on-failure":
		policy = policySlice[0]
		if len(policySlice) > 1 {
			retryCount, err = strconv.Atoi(policySlice[1])
			if err != nil {
				return nil, fmt.Errorf("invalid max retry count: %s", policySlice[1])
			}
		}
	default:
		return nil, fmt.Errorf("restart policy %q not supported", policy)
	}
	return &Policy{
		name:              policy,
		maximumRetryCount: retryCount,
	}, nil
}

func (rp *Policy) String() string {
	if rp.maximumRetryCount > 0 {
		return fmt.Sprintf("%s:%d", rp.name, rp.maximumRetryCount)
	}
	return rp.name
}

func (rp *Policy) Name() string {
	return rp.name
}

func (rp *Policy) MaximumRetryCount() int {
	return rp.maximumRetryCount
}

// Reconcile reconciles the restart policy of a container.
func Reconcile(status containerd.Status, labels map[string]string) bool {
	rp, err := NewPolicy(labels[PolicyLabel])
	if err != nil {
		log.L.WithError(err).Error("policy reconcile")
		return false
	}
	switch rp.Name() {
	case "", "always":
		return true
	case "on-failure":
		restartCount, err := strconv.Atoi(labels[CountLabel])
		if err != nil && labels[CountLabel] != "" {
			log.L.WithError(err).Error("policy reconcile")
			return false
		}
		if status.ExitStatus != 0 && (rp.maximumRetryCount == 0 || restartCount < rp.maximumRetryCount) {
			return true
		}
	case "unless-stopped":
		explicitlyStopped, _ := strconv.ParseBool(labels[ExplicitlyStoppedLabel])
		if !explicitlyStopped {
			return true
		}
	}
	return false
}

// WithLogURI sets the specified log uri for a container.
func WithLogURI(uri *url.URL) func(context.Context, *containerd.Client, *containers.Container) error {
	return WithLogURIString(uri.String())
}

// WithLogURIString sets the specified log uri string for a container.
func WithLogURIString(uriString string) func(context.Context, *containerd.Client, *containers.Container) error {
	return func(_ context.Context, _ *containerd.Client, c *containers.Container) error {
		ensureLabels(c)
		c.Labels[LogURILabel] = uriString
		return nil
	}
}

// WithBinaryLogURI sets the binary-type log uri for a container.
//
// Deprecated(in release 1.5): use WithLogURI
func WithBinaryLogURI(binary string, args map[string]string) func(context.Context, *containerd.Client, *containers.Container) error {
	uri, err := cio.LogURIGenerator("binary", binary, args)
	if err != nil {
		return func(context.Context, *containerd.Client, *containers.Container) error {
			return err
		}
	}
	return WithLogURI(uri)
}

// WithFileLogURI sets the file-type log uri for a container.
//
// Deprecated(in release 1.5): use WithLogURI
func WithFileLogURI(path string) func(context.Context, *containerd.Client, *containers.Container) error {
	uri, err := cio.LogURIGenerator("file", path, nil)
	if err != nil {
		return func(context.Context, *containerd.Client, *containers.Container) error {
			return err
		}
	}
	return WithLogURI(uri)
}

// WithStatus sets the status for a container
func WithStatus(status containerd.ProcessStatus) func(context.Context, *containerd.Client, *containers.Container) error {
	return func(_ context.Context, _ *containerd.Client, c *containers.Container) error {
		ensureLabels(c)
		c.Labels[StatusLabel] = string(status)
		return nil
	}
}

// WithPolicy sets the restart policy for a container
func WithPolicy(policy *Policy) func(context.Context, *containerd.Client, *containers.Container) error {
	return func(_ context.Context, _ *containerd.Client, c *containers.Container) error {
		ensureLabels(c)
		c.Labels[PolicyLabel] = policy.String()
		return nil
	}
}

// WithNoRestarts clears any restart information from the container
func WithNoRestarts(_ context.Context, _ *containerd.Client, c *containers.Container) error {
	if c.Labels == nil {
		return nil
	}
	delete(c.Labels, StatusLabel)
	delete(c.Labels, PolicyLabel)
	delete(c.Labels, LogURILabel)
	return nil
}

func ensureLabels(c *containers.Container) {
	if c.Labels == nil {
		c.Labels = make(map[string]string)
	}
}
