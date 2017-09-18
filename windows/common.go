// +build windows

package windows

import (
	"context"
	"time"

	"github.com/Microsoft/hcsshim"
	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/log"
)

const (
	layerFolderStoreID       = "windowsLayerStore"
	defaultTerminateDuration = 5 * time.Minute
)

type cleanupFunc func(context.Context, string) error

func cleanupRunningContainers(ctx context.Context, db *bolt.DB, owner string, cleanup cleanupFunc) {
	cp, err := hcsshim.GetContainers(hcsshim.ComputeSystemQuery{
		Types:  []string{"Container"},
		Owners: []string{owner},
	})
	if err != nil {
		log.G(ctx).Warn("failed to retrieve running containers")
		return
	}

	for _, p := range cp {
		container, err := hcsshim.OpenContainer(p.ID)
		if err != nil {
			log.G(ctx).Warnf("failed open container %s", p.ID)
			continue
		}

		err = container.Terminate()
		if err == nil || hcsshim.IsPending(err) || hcsshim.IsAlreadyStopped(err) {
			container.Wait()
		}
		container.Close()

		// TODO: remove this once we have a windows snapshotter
		var layerFolderPath string
		if err := db.View(func(tx *bolt.Tx) error {
			s := newLayerFolderStore(tx)
			l, e := s.Get(p.ID)
			if err == nil {
				layerFolderPath = l
			}
			return e
		}); err == nil && layerFolderPath != "" {
			cleanup(ctx, layerFolderPath)
			if dbErr := db.Update(func(tx *bolt.Tx) error {
				s := newLayerFolderStore(tx)
				return s.Delete(p.ID)
			}); dbErr != nil {
				log.G(ctx).WithField("id", p.ID).
					Error("failed to remove key from metadata")
			}
		} else {
			log.G(ctx).WithField("id", p.ID).
				Debug("key not found in metadata, R/W layer may be leaked")
		}

	}
}
