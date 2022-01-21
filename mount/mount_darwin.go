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

package mount

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
)

var (
	// ErrNotImplementOnUnix is returned for methods that are not implemented
	ErrNotImplementOnUnix = errors.New("not implemented under darwin")
	// allowedHelperBinaries is a list of third-party executable, which can
	// be extended in future uses, e.g., mount_nfs, mount_9p.  `mount_darwin`
	// is a special case where we will use the internal darwin snapshotter.
	allowedHelperBinaries = []string{"mount_containerd_darwin"}
)

// Mount to the provided target path.
//
func (m *Mount) Mount(target string) error {
	log.L.Debugf("mount: %s, src %s, type %s", target, m.Source, m.Type)

	binary := fmt.Sprintf("mount_containerd_%s", m.Type)
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("mount binary not found: '%s(%s)': %w", m.Type, binary, errdefs.ErrNotFound)
	}

	for _, helperBinary := range allowedHelperBinaries {
		if binary == helperBinary {
			return mountWithHelper(binary, target, m.Source, m.Options)
		}
	}

	return fmt.Errorf("mount binary isn't allowed to use: '%s(%s)': %w", m.Type, binary, ErrNotImplementOnUnix)
}

// Unmount the provided mount path with the flags
func Unmount(target string, flags int) error {
	return unmount(target, flags)
}

// UnmountAll the provided mount path with the flags
func UnmountAll(target string, flags int) error {
	return unmount(target, flags)
}

func unmount(target string, flags int) error {
	log.L.Debugf("unmount: %s", target)

	if !isMounted(target) {
		log.L.Debugf("%s is already unmounted. skipped.", target)
		return nil
	}

	return unmountWithHelper("mount_containerd_darwin", target, flags)
}

func isMounted(target string) bool {
	// if target is no longer resolvable, treat it as unmounted
	_, err := os.Stat(target)
	if os.IsNotExist(err) {
		log.L.Debugf("%s no longer exists", target)
		return false
	}

	// resolve realpath(3?) for mountinfo
	targetP, err := filepath.EvalSymlinks(target)
	if err != nil {
		log.L.Debugf("failed to resolve target: %s", target)
		return false
	}

	mInfo, err := Lookup(targetP)
	if err != nil {
		log.L.Debugf("failed to lookup mount information: %s", targetP)
		return false
	}

	if mInfo.Mountpoint == "/" {
		log.L.Debugf("mount point is root '/'. skipped: %s", targetP)
		return false
	}

	log.L.Debugf("Mountinfo: %+v\n", mInfo)
	return true
}

func mountWithHelper(helperBinary, target, source string, options []string) error {
	var err error

	args := []string{
		"mount",
		target,
		source,
	}
	args = append(args, options...)
	cmd := exec.Command(helperBinary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}

	return nil
}

func unmountWithHelper(helperBinary, target string, flags int) error {
	var err error

	cmd := exec.Command(helperBinary, "unmount", target, strconv.Itoa(flags))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}

	return nil
}
