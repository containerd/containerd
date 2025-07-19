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

package watchdog

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/services/healthcheck"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/coreos/go-systemd/v22/daemon"
)

type service struct {
	wb *watchdogNotifier
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.WatchdogPlugin,
		ID:   "watchdog",
		Requires: []plugin.Type{
			plugins.GRPCPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			hcp, err := ic.GetByID(plugins.GRPCPlugin, "healthcheck")
			if err != nil {
				return nil, err
			}
			hc := hcp.(*healthcheck.Service)
			wn, err := newWatchdogNotifier(hc.IsServing)
			if err != nil {
				return nil, fmt.Errorf("cannot init watchdog plugin: %v: %w", err, plugin.ErrSkipPlugin)
			}
			// start notifying
			go wn.Notify(ic.Context)
			return &service{wn}, nil
		},
	})
}

type watchdogNotifier struct {
	wc       watchdogClient
	interval time.Duration
	healthy  func(ctx context.Context) (bool, error)
	done     chan struct{}
}

// option defines optional parameters for initializing the healthChecker
// structure.
type option func(*watchdogNotifier)

func withWatchdogClient(wc watchdogClient) option {
	return func(wn *watchdogNotifier) {
		wn.wc = wc
	}
}

// newWatchdogNotifier creates a new watchogNotifier.
//
// If the watchdog is not enabled, the function returns an error.
func newWatchdogNotifier(healthy func(ctx context.Context) (bool, error), opts ...option) (*watchdogNotifier, error) {
	wn := &watchdogNotifier{
		healthy: healthy,
		wc:      &defaultWatchdogClient{},
		done:    make(chan struct{}),
	}
	for _, o := range opts {
		o(wn)
	}

	watchdogVal, err := wn.wc.sdWatchdogEnabled()
	switch {
	case err != nil:
		return nil, fmt.Errorf("cannot determine if watchdog is enabled: %v", err)
	case watchdogVal == 0:
		return nil, fmt.Errorf("systemd watchdog not enabled for this PID")
	}
	// It is recommended that a daemon sends a keep-alive notification
	// message to the service manager every half of the time of
	// WATCHDOG_USEC. See:
	// https://www.freedesktop.org/software/systemd/man/latest/sd_watchdog_enabled.html
	wn.interval = watchdogVal / 2
	return wn, nil
}

func (wn *watchdogNotifier) Notify(ctx context.Context) {
	ticker := time.NewTicker(wn.interval)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-ticker.C:
			isServing, err := wn.healthy(ctx)
			if err != nil {
				log.G(ctx).WithError(err).Error("cannot determine if the daemon is healthy")
				break loop
			}
			if !isServing {
				log.G(ctx).Warn("daemon detected as unhealthy")
				break loop
			}
			ack, err := wn.wc.sdNotify()
			if err != nil {
				// Notification not supported. This should never happen.
				log.G(ctx).WithError(err).Errorf("cannot notify systemd watchdog: %v", err)
				break loop
			}
			if !ack {
				log.G(ctx).Warn("cannot notify systemd watchdog. retrying")
				// let some time pass to let systemd recover
				const p = int64(5)
				time.Sleep(time.Duration(int64(wn.interval) / p))
			}
		case <-ctx.Done():
			break loop
		}
	}
	log.G(ctx).Warn("terminating watchdog notifier")
	close(wn.done)
}

func (wn *watchdogNotifier) Wait() {
	<-wn.done
}

// watchdogClient represents the interaction with systemd watchdog.
type watchdogClient interface {
	sdWatchdogEnabled() (time.Duration, error)
	sdNotify() (bool, error)
}

type defaultWatchdogClient struct{}

var _ watchdogClient = &defaultWatchdogClient{}

func (d *defaultWatchdogClient) sdWatchdogEnabled() (time.Duration, error) {
	return daemon.SdWatchdogEnabled(false)
}

func (d *defaultWatchdogClient) sdNotify() (bool, error) {
	return daemon.SdNotify(false, daemon.SdNotifyWatchdog)
}
