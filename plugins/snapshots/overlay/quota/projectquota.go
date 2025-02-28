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

/*
   Copyright 2013-2018 Docker, Inc.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       https://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

//
// projectquota.go - implements XFS project quota controls
// for setting quota limits on a newly created directory.
// It currently supports the legacy XFS specific ioctls.
//
// TODO: use generic quota control ioctl FS_IOC_FS{GET,SET}XATTR
//       for both xfs/ext4 for kernel version >= v4.5
//

package quota

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/containerd/log"
)

// Quota limit params - currently we only control blocks hard limit
type Size uint64

// Control - Context to be used by storage driver (e.g. overlay)
// who wants to apply project quotas to container dirs
type Control struct {
	sync.RWMutex
	backingFsBlockDev string
	nextProjectID     uint32
	quotas            map[string]uint32
}

// NewControl - initialize project quota support.
// Test to make sure that quota can be set on a test dir and find
// the first project id to be used for the next container create.
//
// Returns nil (and error) if project quota is not supported.
//
// First get the project id of the home directory.
// This test will fail if the backing fs is not xfs.
//
// xfs_quota tool can be used to assign a project id to the driver home directory, e.g.:
//
//	echo 999:/var/lib/docker/overlay2 >> /etc/projects
//	echo docker:999 >> /etc/projid
//	xfs_quota -x -c 'project -s docker' /<xfs mount point>
//
// In that case, the home directory project id will be used as a "start offset"
// and all containers will be assigned larger project ids (e.g. >= 1000).
// This is a way to prevent xfs_quota management from conflicting with docker.
//
// Then try to create a test directory with the next project id and set a quota
// on it. If that works, continue to scan existing containers to map allocated
// project ids.
func NewControl(basePath string) (*Control, error) {
	//
	// Get project id of parent dir as minimal id to be used by driver
	//
	minProjectID, err := getProjectID(basePath)
	if err != nil {
		return nil, err
	}
	minProjectID++

	//
	// create backing filesystem device node
	//
	backingFsBlockDev, err := makeBackingFsDev(basePath)
	if err != nil {
		return nil, err
	}

	//
	// Test if filesystem supports project quotas by trying to set
	// a quota on the first available project id
	//
	if err := setProjectQuota(backingFsBlockDev, minProjectID, 0); err != nil {
		return nil, err
	}

	q := Control{
		backingFsBlockDev: backingFsBlockDev,
		nextProjectID:     minProjectID + 1,
		quotas:            make(map[string]uint32),
	}

	//
	// get first project id to be used for next container
	//
	err = q.findNextProjectID(basePath)
	if err != nil {
		return nil, err
	}
	log.G(context.TODO()).Debugf("NewControl(%s): nextProjectID = %d", basePath, q.nextProjectID)
	return &q, nil
}

// SetAllQuota - assign a unique project id to every directory and set the quota limits
// for that project id
func (q *Control) SetAllQuota(quota Size, targetPaths ...string) (rerr error) {
	var (
		err          error
		successPaths []string
		oneID        uint32
	)
	defer func() {
		if rerr != nil {
			for _, tpath := range successPaths {
				delete(q.quotas, tpath)
			}
		}
	}()
	q.Lock()
	defer q.Unlock()
	for _, targetPath := range targetPaths {
		projectID, ok := q.quotas[targetPath]
		if !ok {
			if oneID != 0 {
				projectID = oneID
			} else {
				projectID = q.nextProjectID
			}
			//
			// assign project id to new container directory
			//
			err := setProjectID(targetPath, projectID)
			if err != nil {
				return err
			}
		} else {
			if oneID != 0 {
				projectID = oneID
			}
		}
		oneID = projectID
		q.quotas[targetPath] = projectID
		successPaths = append(successPaths, targetPath)
		//
		// set the quota limit for the container's project id
		//
		log.G(context.TODO()).Debugf("SetQuota(%s, %d): projectID=%d", targetPath, quota, projectID)
		err = setProjectQuota(q.backingFsBlockDev, oneID, quota)
		if err != nil {
			return err
		}
	}
	q.nextProjectID++
	return nil
}

// SetQuota - assign a unique project id to directory and set the quota limits
// for that project id
func (q *Control) SetQuota(quota Size, targetPath string) error {
	q.Lock()
	defer q.Unlock()
	projectID, ok := q.quotas[targetPath]
	if !ok {
		projectID = q.nextProjectID

		//
		// assign project id to new container directory
		//
		err := setProjectID(targetPath, projectID)
		if err != nil {
			return err
		}

		q.quotas[targetPath] = projectID
		q.nextProjectID++
	}

	//
	// set the quota limit for the container's project id
	//
	log.G(context.TODO()).Debugf("SetQuota(%s, %d): projectID=%d", targetPath, quota, projectID)
	return setProjectQuota(q.backingFsBlockDev, projectID, quota)
}

// setProjectQuota - set the quota for project id on xfs block device
func setProjectQuota(backingFsBlockDev string, projectID uint32, quota Size) error {
	return SetProjectQuota(backingFsBlockDev, projectID, quota)
}

// GetQuota - get the quota limits of a directory that was configured with SetQuota
func (q *Control) GetQuota(targetPath string) (Size, error) {
	q.RLock()
	projectID, ok := q.quotas[targetPath]
	if !ok {
		q.RUnlock()
		return 0, fmt.Errorf("quota not found for path : %s", targetPath)
	}
	q.RUnlock()
	return GetProjectQuota(q.backingFsBlockDev, projectID)
}

// findNextProjectID - find the next project id to be used for containers
// by scanning driver home directory to find used project ids
func (q *Control) findNextProjectID(home string) error {
	files, err := os.ReadDir(home)
	if err != nil {
		return fmt.Errorf("read directory failed : %s", home)
	}
	for _, file := range files {
		if !file.IsDir() {
			continue
		}
		path := filepath.Join(home, file.Name())
		projid, err := getProjectID(path)
		if err != nil {
			return err
		}
		if projid > 0 {
			q.quotas[path] = projid
		}
		if q.nextProjectID <= projid {
			q.nextProjectID = projid + 1
		}
	}
	return nil
}

// Get the backing block device of the driver home directory
// and create a block device node under the home directory
// to be used by quotactl commands
func makeBackingFsDev(home string) (string, error) {
	fileinfo, err := os.Stat(home)
	if err != nil {
		return "", err
	}

	backingFsBlockDev := path.Join(home, "backingFsBlockDev")
	// Re-create just in case comeone copied the home directory over to a new device
	syscall.Unlink(backingFsBlockDev)
	stat := fileinfo.Sys().(*syscall.Stat_t)
	if err := syscall.Mknod(backingFsBlockDev, syscall.S_IFBLK|0600, int(stat.Dev)); err != nil {
		return "", fmt.Errorf("failed to mknod %s: %v", backingFsBlockDev, err)
	}

	return backingFsBlockDev, nil
}
