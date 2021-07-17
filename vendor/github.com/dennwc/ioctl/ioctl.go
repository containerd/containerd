package ioctl

import (
	"fmt"
	"os"
	"reflect"
	"syscall"
)

const (
	nrBits   = 8
	typeBits = 8
	sizeBits = 14
	dirBits  = 2
)

const (
	nrMask   = (1 << nrBits) - 1
	typeMask = (1 << typeBits) - 1
	sizeMask = (1 << sizeBits) - 1
	dirMask  = (1 << dirBits) - 1
)

const (
	nrShift   = 0
	typeShift = nrShift + nrBits
	sizeShift = typeShift + typeBits
	dirShift  = sizeShift + sizeBits
)

const (
	None  = 0
	Write = 1
	Read  = 2
)

func IOC(dir, typ, nr, size uintptr) uintptr {
	return (dir << dirShift) |
		(typ << typeShift) |
		(nr << nrShift) |
		(size << sizeShift)
}

func IO(typ, nr uintptr) uintptr {
	return IOC(None, typ, nr, 0)
}

func IOR(typ, nr, size uintptr) uintptr {
	return IOC(Read, typ, nr, size)
}

func IOW(typ, nr, size uintptr) uintptr {
	return IOC(Write, typ, nr, size)
}

func IOWR(typ, nr, size uintptr) uintptr {
	return IOC(Read|Write, typ, nr, size)
}

func Ioctl(f *os.File, ioc uintptr, addr uintptr) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioc, addr)
	if e != 0 {
		return e
	}
	return nil
}

func Do(f *os.File, ioc uintptr, arg interface{}) error {
	var addr uintptr
	if arg != nil {
		v := reflect.ValueOf(arg)
		switch v.Kind() {
		case reflect.Ptr:
			addr = v.Elem().UnsafeAddr()
		case reflect.Slice:
			addr = v.Index(0).UnsafeAddr()
		default:
			return fmt.Errorf("expected ptr or slice, got %T", arg)
		}
	}
	return Ioctl(f, ioc, addr)
}
