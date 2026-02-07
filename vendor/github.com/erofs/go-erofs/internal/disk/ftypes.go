package disk

import "io/fs"

const (
	FileTypeReg     = 1
	FileTypeDir     = 2
	FileTypeChrdev  = 3
	FileTypeBlkdev  = 4
	FileTypeFifo    = 5
	FileTypeSock    = 6
	FileTypeSymlink = 7

	StatTypeMask    = 0170000 // Mask for the type bits
	StatTypeReg     = 0100000 // Regular file
	StatTypeDir     = 0040000 // Directory
	StatTypeChrdev  = 0020000 // Character device
	StatTypeBlkdev  = 0060000 // Block device
	StatTypeFifo    = 0010000 // FIFO
	StatTypeSock    = 0140000 // Socket
	StatTypeSymlink = 0120000 // Symlink
	StatTypeIsUID   = 0004000 // Setuid on execution
	StatTypeIsGID   = 0002000 // Setgid on execution
	StatTypeIsVTX   = 0001000 // Sticky bit
)

// Converts EroFS filetypes to Go FileMode
func EroFSFtypeToFileMode(ftype uint8) fs.FileMode {
	switch ftype {
	case FileTypeDir:
		return fs.ModeDir
	case FileTypeChrdev:
		return fs.ModeCharDevice
	case FileTypeBlkdev:
		return fs.ModeDevice
	case FileTypeFifo:
		return fs.ModeNamedPipe
	case FileTypeSock:
		return fs.ModeSocket
	case FileTypeSymlink:
		return fs.ModeSymlink
	default:
		return 0
	}
}

func EroFSModeToGoFileMode(mode uint16) fs.FileMode {
	var m fs.FileMode
	m |= fs.FileMode(mode & 0777)
	switch mode & StatTypeMask {
	case StatTypeReg:
	case StatTypeDir:
		m |= fs.ModeDir
	case StatTypeChrdev:
		m |= fs.ModeCharDevice
	case StatTypeBlkdev:
		m |= fs.ModeDevice
	case StatTypeFifo:
		m |= fs.ModeNamedPipe
	case StatTypeSock:
		m |= fs.ModeSocket
	case StatTypeSymlink:
		m |= fs.ModeSymlink
	default:
		m |= fs.ModeIrregular // Unknown type, treat as irregular file
	}
	if mode&StatTypeIsUID != 0 {
		m |= fs.ModeSetuid
	}
	if mode&StatTypeIsGID != 0 {
		m |= fs.ModeSetgid
	}
	if mode&StatTypeIsVTX != 0 {
		m |= fs.ModeSticky
	}

	return m
}

func RdevFromMode(mode uint16, inodeData uint32) uint32 {
	switch mode & StatTypeMask {
	case StatTypeChrdev, StatTypeBlkdev, StatTypeFifo, StatTypeSock:
		// inodeData field is device number for some file types
		return inodeData
	default:
		return 0 // Not a device type
	}
}
