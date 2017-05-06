// +build !windows,!linux,!freebsd,!solaris freebsd,!cgo solaris,!cgo

package mountinfo

import (
	"fmt"
	"runtime"
)

func parseMountTable() ([]*Info, error) {
	return nil, fmt.Errorf("mount.parseMountTable is not implemented on %s/%s", runtime.GOOS, runtime.GOARCH)
}
