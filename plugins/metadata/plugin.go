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
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/events"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/timeout"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	bolt "go.etcd.io/bbolt"
)

const (
	boltOpenTimeout = "io.containerd.timeout.bolt.open"
)

func init() {
	timeout.Set(boltOpenTimeout, 0) // set to 0 means to wait indefinitely for bolt.Open
}

// BoltOptions replicates bolt.Options struct, but excludes function fields and adds tags to allow TOML marshalling.
type BoltOptions struct {
	//  NoGrowSync sets the DB.NoGrowSync flag before memory mapping the file.
	NoGrowSync bool `toml:"no_grow_sync"`
	// NoFreelistSync disables sync freelist to disk. This improves the database write performance
	// under normal operation, but requires a full database re-sync during recovery.
	NoFreelistSync bool `toml:"no_freelist_sync"`
	// PreLoadFreelist sets whether to load the free pages when opening
	// the db file. Note when opening db in write mode, bbolt will always
	// load the free pages.
	PreLoadFreelist bool `toml:"preload_freelist"`
	// NoSync sets the initial value of DB.NoSync. Normally this can just be
	// set directly on the DB itself when returned from Open(), but this option
	// is useful in APIs which expose Options but not the underlying DB.
	NoSync bool `toml:"no_sync"`
	// Mlock locks database file in memory when set to true.
	// It prevents potential page faults, however
	// used memory can't be reclaimed. (UNIX only)
	Mlock bool `toml:"mlock"`
	// MmapFlags sets the DB.MmapFlags flag before memory mapping the file.
	MmapFlags int `toml:"mmap_flags"`
	// InitialMmapSize is the initial mmap size of the database
	// in bytes. Read transactions won't block write transaction
	// if the InitialMmapSize is large enough to hold database mmap
	// size. (See DB.Begin for more information)
	InitialMmapSize int `toml:"init_mmap_size"`
	// PageSize overrides the default OS page size.
	PageSize int `toml:"page_size"`
}

func defaultBoltOptions() BoltOptions {
	opts := bolt.DefaultOptions
	return BoltOptions{
		// Reading bbolt's freelist sometimes fails when the file has a data corruption.
		// Disabling freelist sync reduces the chance of the breakage.
		// https://github.com/etcd-io/bbolt/pull/1
		// https://github.com/etcd-io/bbolt/pull/6
		NoFreelistSync:  true,
		NoGrowSync:      opts.NoGrowSync,
		PreLoadFreelist: opts.PreLoadFreelist,
		NoSync:          opts.NoSync,
		Mlock:           opts.Mlock,
		MmapFlags:       opts.MmapFlags,
		InitialMmapSize: opts.InitialMmapSize,
		PageSize:        opts.PageSize,
	}
}

// BoltConfig defines the configuration values for the bolt plugin, which is
// loaded here, rather than back registered in the metadata package.
type BoltConfig struct {
	// ContentSharingPolicy sets the sharing policy for content between
	// namespaces.
	//
	// The default mode "shared" will make blobs available in all
	// namespaces once it is pulled into any namespace. The blob will be pulled
	// into the namespace if a writer is opened with the "Expected" digest that
	// is already present in the backend.
	//
	// The alternative mode, "isolated" requires that clients prove they have
	// access to the content by providing all of the content to the ingest
	// before the blob is added to the namespace.
	//
	// Both modes share backing data, while "shared" will reduce total
	// bandwidth across namespaces, at the cost of allowing access to any blob
	// just by knowing its digest.
	ContentSharingPolicy string `toml:"content_sharing_policy"`

	// DB allows low-level tuning of how DB writes database to disk.
	DB BoltOptions `toml:"db"`
}

const (
	// SharingPolicyShared represents the "shared" sharing policy
	SharingPolicyShared = "shared"
	// SharingPolicyIsolated represents the "isolated" sharing policy
	SharingPolicyIsolated = "isolated"
)

// Validate validates if BoltConfig is valid
func (bc *BoltConfig) Validate() error {
	switch bc.ContentSharingPolicy {
	case SharingPolicyShared, SharingPolicyIsolated:
		return nil
	default:
		return fmt.Errorf("unknown policy: %s: %w", bc.ContentSharingPolicy, errdefs.ErrInvalidArgument)
	}
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.MetadataPlugin,
		ID:   "bolt",
		Requires: []plugin.Type{
			plugins.ContentPlugin,
			plugins.EventPlugin,
			plugins.SnapshotPlugin,
		},
		Config: &BoltConfig{
			ContentSharingPolicy: SharingPolicyShared,
			DB:                   defaultBoltOptions(),
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			root := ic.Properties[plugins.PropertyRootDir]
			if err := os.MkdirAll(root, 0711); err != nil {
				return nil, err
			}
			cs, err := ic.GetSingle(plugins.ContentPlugin)
			if err != nil {
				return nil, err
			}

			snapshottersRaw, err := ic.GetByType(plugins.SnapshotPlugin)
			if err != nil {
				return nil, err
			}

			snapshotters := make(map[string]snapshots.Snapshotter)
			for name, sn := range snapshottersRaw {
				snapshotters[name] = sn.(snapshots.Snapshotter)
			}

			ep, err := ic.GetSingle(plugins.EventPlugin)
			if err != nil {
				return nil, err
			}

			config := *bolt.DefaultOptions
			// Without the timeout, bbolt.Open would block indefinitely due to flock(2).
			config.Timeout = timeout.Get(boltOpenTimeout)

			shared := true
			ic.Meta.Exports["policy"] = SharingPolicyShared
			if cfg, ok := ic.Config.(*BoltConfig); ok {
				if cfg.ContentSharingPolicy != "" {
					if err := cfg.Validate(); err != nil {
						return nil, err
					}
					if cfg.ContentSharingPolicy == SharingPolicyIsolated {
						ic.Meta.Exports["policy"] = SharingPolicyIsolated
						shared = false
					}

					log.G(ic.Context).WithField("policy", cfg.ContentSharingPolicy).Info("metadata content store policy set")

					// Apply options from config
					config.NoFreelistSync = cfg.DB.NoFreelistSync
					config.PreLoadFreelist = cfg.DB.PreLoadFreelist
					config.NoSync = cfg.DB.NoSync
					config.Mlock = cfg.DB.Mlock
					config.NoGrowSync = cfg.DB.NoGrowSync
					config.MmapFlags = cfg.DB.MmapFlags
					config.InitialMmapSize = cfg.DB.InitialMmapSize
					config.PageSize = cfg.DB.PageSize
				}
			}
			log.G(ic.Context).WithField("plugin", "bolt").Infof("bolt config: %+v", config)

			path := filepath.Join(root, "meta.db")
			ic.Meta.Exports["path"] = path

			doneCh := make(chan struct{})
			go func() {
				t := time.NewTimer(10 * time.Second)
				defer t.Stop()
				select {
				case <-t.C:
					log.G(ic.Context).WithField("plugin", "bolt").Warn("waiting for response from boltdb open")
				case <-doneCh:
					return
				}
			}()

			db, err := bolt.Open(path, 0644, &config)
			close(doneCh)
			if err != nil {
				return nil, err
			}

			dbopts := []metadata.DBOpt{
				metadata.WithEventsPublisher(ep.(events.Publisher)),
			}

			if !shared {
				dbopts = append(dbopts, metadata.WithPolicyIsolated)
			}

			mdb := metadata.NewDB(db, cs.(content.Store), snapshotters, dbopts...)
			if err := mdb.Init(ic.Context); err != nil {
				return nil, err
			}
			return mdb, nil
		},
	})
}
