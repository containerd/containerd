//go:build !darwin

package fuse

import "fmt"

func (in *CreateIn) string() string {
	return fmt.Sprintf(
		"{0%o [%s] (0%o)}", in.Mode,
		flagString(openFlagNames, int64(in.Flags), "O_RDONLY"), in.Umask)
}

func (in *GetAttrIn) string() string {
	return fmt.Sprintf("{Fh %d %s}", in.Fh_, flagString(getAttrFlagNames, int64(in.Flags_), ""))
}

func (in *MknodIn) string() string {
	return fmt.Sprintf("{0%o (0%o), %d}", in.Mode, in.Umask, in.Rdev)
}

func (in *ReadIn) string() string {
	return fmt.Sprintf("{Fh %d [%d +%d) %s L %d %s}",
		in.Fh, in.Offset, in.Size,
		flagString(readFlagNames, int64(in.ReadFlags), ""),
		in.LockOwner,
		flagString(openFlagNames, int64(in.Flags), "RDONLY"))
}

func (in *WriteIn) string() string {
	return fmt.Sprintf("{Fh %d [%d +%d) %s L %d %s}",
		in.Fh, in.Offset, in.Size,
		flagString(writeFlagNames, int64(in.WriteFlags), ""),
		in.LockOwner,
		flagString(openFlagNames, int64(in.Flags), "RDONLY"))
}
