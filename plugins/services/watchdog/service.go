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
	"fmt"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/coreos/go-systemd/v22/daemon"
)

func init() {
	registry.Register(&plugin.Registration{
		Type:     plugins.WatchdogPlugin,
		ID:       "systemd-watchdog",
		Requires: []plugin.Type{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			if runtime.GOOS != "linux" {
				return nil, fmt.Errorf("host does not support watchdog: %w", plugin.ErrSkipPlugin)
			}
			// system manager sets the WATCHDOG_USEC variable
			// https://0pointer.de/blog/projects/watchdog.html
			watchdogUsec := os.Getenv("WATCHDOG_USEC")
			fmt.Println("WATCHDOG_USEC:", watchdogUsec)
			if watchdogUsec == "" {
				return nil, fmt.Errorf("no watchdog interval is configured: %w", plugin.ErrSkipPlugin)
			}
			watchdogInt, err := strconv.Atoi(watchdogUsec)
			if err != nil {
				return nil, fmt.Errorf("error converting WATCHDOG_USEC: %w", plugin.ErrSkipPlugin)
			}
			watchdogInterval := time.Duration(watchdogInt/2) * time.Microsecond
			// Start a Go routine to periodically notify systemd
			go notifyWatchdog(watchdogInterval)
			return &service{}, nil
		},
	})
}

type service struct {
}

func notifyWatchdog(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		ack, err := daemon.SdNotify(false, daemon.SdNotifyWatchdog)
		if err != nil {
			fmt.Println("Failed to notify systemd watchdog - ", err)
		}
		fmt.Println("Sent watchdog notification -", ack)
	}
}

func NotifyReady(){
	ack, err := daemon.SdNotify(false, daemon.SdNotifyReady)
	if err != nil {
		fmt.Println("Failed to notify systemd ready - ", err)
	}
	fmt.Println("Notified systemd ready - ", ack)
}