package cgroups

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// CgroupID implements mapping kernel cgroup IDs to cgroupfs paths with transparent caching.
type CgroupID struct {
	root  string
	cache map[uint64]string
	sync.Mutex
}

// NewCgroupID creates a new CgroupID map/cache.
func NewCgroupID(root string) *CgroupID {
	return &CgroupID{
		root:  root,
		cache: make(map[uint64]string),
	}
}

// Find finds the path for the given cgroup id.
func (cgid *CgroupID) Find(id uint64) (string, error) {
	found := false
	var p string

	cgid.Lock()
	defer cgid.Unlock()

	if path, ok := cgid.cache[id]; ok {
		return path, nil
	}

	err := fsi.Walk(cgid.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}

		if found {
			return filepath.SkipDir
		}

		if info.IsDir() && id == getID(path) {
			found = true
			p = path
			return filepath.SkipDir
		}
		return nil
	})

	if err != nil {
		return "", err
	} else if !found {
		return "", fmt.Errorf("cgroupid %v not found", id)
	} else {
		cgid.cache[id] = p
		return p, nil
	}
}
