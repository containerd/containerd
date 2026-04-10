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

package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/internal/tomlext"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	bolt "go.etcd.io/bbolt"
)

var errWatchdogHealthCheckDone = errors.New("watchdog: content store health check done")

type Config struct {
	HealthCheckTimeout tomlext.Duration `toml:"health_check_timeout"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.InternalPlugin,
		ID:   "watchdog",
		Requires: []plugin.Type{
			plugins.MetadataPlugin,
			plugins.ContentPlugin,
		},
		//Default configuration for Timeout
		Config: &Config{
			HealthCheckTimeout:tomlext.FromStdTime(5 * time.Second),
		},
		InitFn: func(ic *plugin.InitContext) (any,error) {
			mdRaw, err := ic.GetSingle(plugins.MetadataPlugin)
			if err != nil {
				return nil, fmt.Errorf("watchdog: failed to get metadata plugin: %w",err)
			}
			mdb := mdRaw.(*metadata.DB)

			csRaw, err := ic.GetSingle(plugins.ContentPlugin)
			if err != nil {
				return nil, fmt.Errorf("watchdog: failed to get content plugin: %w",err)
			}
			cs := csRaw.(content.Store)

			cfg := ic.Config.(*Config)
			w := &watchdog{
				mdb: mdb,
				cs: cs,
				timeout: tomlext.ToStdTime(cfg.HealthCheckTimeout),
			}
			go w.run(ic.Context)
			return w, nil
		},
	})
}

type watchdog struct {
	mdb *metadata.DB
	cs content.Store
	timeout time.Duration
}

func (w *watchdog) run(ctx context.Context) {
	interval, err := daemon.SdWatchdogEnabled(false)
	if err != nil {
		log.G(ctx).WithError(err).Error("watchdog: failed to read WATCHDOG_USEC from the env")
		return
	}
	if interval == 0 {
		log.G(ctx).Debug("watchdog: systemd watchdog not enabled")
		return
	}
	pingInterval := interval/2;

	for {
		select{
		case <-ctx.Done():
			log.G(ctx).Debug("watchdog: context cancelled, stopping watchdog loop")
			return
		case <-time.After(pingInterval):
		}
		checkCtx, cancel := context.WithTimeout(ctx, w.timeout)
		healthErr := w.healthCheck(checkCtx)
		cancel()

		if healthErr != nil {
			log.G(ctx).WithError(healthErr).Warn("watchdog: health check failed,")
			continue
		}
		notified, err := daemon.SdNotify(false, daemon.SdNotifyWatchdog)
		if err != nil {
			log.G(ctx).WithError(err).Warn("watchdog: failed to watchdog notifiaction to systemd")
		}else{
			log.G(ctx).WithField("notified",notified).Infof("watchdog: sent WATCHDOG=1 ping")

		}
	}
}

func(w *watchdog) healthCheck(ctx context.Context) error {
	if err := w.checkMetadataDB(ctx); err != nil {
		return fmt.Errorf("metadata store: %w",err)
	}
	if err := w.checkContentStore(ctx); err != nil {
		return fmt.Errorf("content store: %w",err)
	}
	return nil
}

func(w *watchdog) checkMetadataDB(_ context.Context) error {
	return w.mdb.View(func(_ *bolt.Tx)error{
		return nil;
	})
}

func(w *watchdog) checkContentStore(ctx context.Context) error {
	err := w.cs.Walk(ctx, func(_ content.Info)error{
		return errWatchdogHealthCheckDone
	})
	if errors.Is(err, errWatchdogHealthCheckDone){
		return nil
	}
	return err
}