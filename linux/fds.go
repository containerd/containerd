package linux

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/docker/containerd/api/types/container"

	"github.com/pkg/errors"
)

/* github.com/vishvananda/netlink/link_tuntap_linux.go. Apache 2.0 license */
const (
	SizeOfIfReq = 40
	IFNAMSIZ    = 16
)

type ifReq struct {
	Name  [IFNAMSIZ]byte
	Flags uint16
	pad   [SizeOfIfReq - IFNAMSIZ - 2]byte
}

type ifreqHwaddr struct {
	Name   [IFNAMSIZ]byte
	Hwaddr syscall.RawSockaddr
}

func openTapDevice(tap, mac string) (*os.File, error) {
	if len(tap) > IFNAMSIZ-1 {
		return nil, syscall.ENAMETOOLONG
	}
	f, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	var req ifReq

	copy(req.Name[:IFNAMSIZ-1], tap)
	req.Flags = syscall.IFF_TAP | syscall.IFF_NO_PI
	if r, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(),
		uintptr(syscall.TUNSETIFF), uintptr(unsafe.Pointer(&req))); r != 0 {
		return nil, errno
	}

	return f, nil
}

func openExtraFiles(extras []*container.ExtraFd) ([]*os.File, error) {
	var fds []*os.File
	cleanup := func() {
		for _, f := range fds {
			f.Close()
		}
	}

	for _, extra := range extras {
		switch extra.Kind {
		case container.ExtraFd_NULL:
			if len(extra.Args) != 0 {
				cleanup()
				return nil, errors.New("Incorrect number of args for /dev/null extra fd")
			}
			f, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
			if err != nil {
				cleanup()
				return nil, err
			}
			fds = append(fds, f)
		case container.ExtraFd_TAP:
			if len(extra.Args) != 2 {
				cleanup()
				return nil, errors.New("Incorrect number of args for tap extra fd")
			}
			tap := extra.Args[0]
			mac := extra.Args[1]
			f, err := openTapDevice(tap, mac)
			if err != nil {
				cleanup()
				return nil, err
			}
			fds = append(fds, f)
		default:
			cleanup()
			return nil, errors.Errorf("Unknown extrafd: %#v", extra)
		}
	}
	return fds, nil
}
