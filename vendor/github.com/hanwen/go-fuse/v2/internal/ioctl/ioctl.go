package ioctl

// https://github.com/torvalds/linux/blob/master/include/uapi/asm-generic/ioctl.h

const (
	NONE  = 0x0
	READ  = 0x1
	WRITE = 0x2
)

// The ioctl command. It encodes direction (read/write), argument size
// and a type/number, where type should be globally unique and number
// is unique to the driver.
type Command uint32

// New constructs an ioctl command. size should be less than 16kb.
func New(dir byte, typ byte, nr byte, size uintptr) Command {
	if size >= (1 << 14) {
		panic("invalid ioctl sizeof")
	}
	return Command(dir)<<(14+16) |
		Command(size)<<16 |
		Command(typ)<<8 |
		Command(nr)
}

// Read returns true if the ioctl reads data
func (c Command) Read() bool {
	return (c>>(14+16))&READ != 0
}

// Write returns true if the ioctl writes data
func (c Command) Write() bool {
	return (c>>(14+16))&WRITE != 0
}

// Number returns the lower 8 bits of the command
func (c Command) Number() byte {
	return byte(c & 0xff)
}

// Type returns the upper 8 bits of the command
func (c Command) Type() byte {
	return byte((c >> 8) & 0xff)
}
