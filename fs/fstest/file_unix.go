// +build !windows

package fstest

import "github.com/containerd/continuity/sysx"

func SetXAttr(name, key, value string) Applier {
	return applyFn(func(root string) error {
		return sysx.LSetxattr(name, key, []byte(value), 0)
	})
}
