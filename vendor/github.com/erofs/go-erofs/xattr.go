package erofs

import (
	"encoding/binary"
	"fmt"

	"github.com/erofs/go-erofs/internal/disk"
)

/*
#define EROFS_XATTR_INDEX_USER              1
#define EROFS_XATTR_INDEX_POSIX_ACL_ACCESS  2
#define EROFS_XATTR_INDEX_POSIX_ACL_DEFAULT 3
#define EROFS_XATTR_INDEX_TRUSTED           4
#define EROFS_XATTR_INDEX_LUSTRE            5
#define EROFS_XATTR_INDEX_SECURITY          6
*/

type xattrIndex uint8

func (idx xattrIndex) String() string {
	switch idx {
	case 1:
		return "user."
	case 2:
		return "system.posix_acl_access."
	case 3:
		return "system.posix_acl_default."
	case 4:
		return "trusted."
	case 5:
		return "lustre."
	case 6:
		return "security."
	default:
		return ""
	}
}

// loadXattrs reads the extended attributes for the file's inode and
// populates the given Stat's Xattrs map.
func loadXattrs(b *file, stat *Stat) (err error) {
	ino := b.info
	addr := b.img.metaStartPos() + int64(ino.nid*disk.SizeInodeCompact) + int64(ino.icsize)
	xsize := ino.xsize

	stat.Xattrs = map[string]string{}

	blk, err := b.img.loadAt(addr, int64(xsize))
	if err != nil {
		return fmt.Errorf("failed to read xattr body for nid %d: %w", b.nid, err)
	}
	defer func() {
		if blk != nil {
			b.img.putBlock(blk)
		}
	}()

	xb := blk.bytes()
	if len(xb) < disk.SizeXattrBodyHeader {
		return fmt.Errorf("xattr body too small for nid %d: %w", b.nid, ErrInvalid)
	}
	var xh disk.XattrHeader
	if _, err := binary.Decode(xb[:disk.SizeXattrBodyHeader], binary.LittleEndian, &xh); err != nil {
		return err
	}
	xb = xb[disk.SizeXattrBodyHeader:]

	for i := 0; i < int(xh.SharedCount); i++ {
		if len(xb) < 4 {
			pos := disk.SizeXattrBodyHeader + int64(i)*4
			b.img.putBlock(blk)
			blk, err = b.img.loadAt(addr+pos, int64(xsize)-pos)
			if err != nil {
				return fmt.Errorf("failed to read xattr body for nid %d: %w", b.nid, err)
			}
			xb = blk.bytes()
			if len(xb) < 4 {
				return fmt.Errorf("xattr shared block too small for nid %d: %w", b.nid, ErrInvalid)
			}
		}
		var xattrAddr uint32
		if _, err := binary.Decode(xb[:4], binary.LittleEndian, &xattrAddr); err != nil {
			return err
		}

		// TODO: Cache shared xattr blocks
		sblk, err := b.img.loadAt(int64(b.img.sb.XattrBlkAddr)<<b.img.sb.BlkSizeBits+int64(xattrAddr*4), int64(1<<b.img.sb.BlkSizeBits))
		if err != nil {
			return fmt.Errorf("failed to read shared xattr body for nid %d: %w", b.nid, err)
		}
		sb := sblk.bytes()
		if len(sb) < disk.SizeXattrEntry {
			b.img.putBlock(sblk)
			return fmt.Errorf("shared xattr block too small for nid %d: %w", b.nid, ErrInvalid)
		}
		var xattrEntry disk.XattrEntry
		if _, err := binary.Decode(sb[:disk.SizeXattrEntry], binary.LittleEndian, &xattrEntry); err != nil {
			b.img.putBlock(sblk)
			return err
		}
		sb = sb[disk.SizeXattrEntry:]
		var prefix string
		if xattrEntry.NameIndex&0x80 == 0x80 {
			// Long prefix: highest bit set
			longPrefixIndex := xattrEntry.NameIndex & 0x7F
			prefix, err = b.img.getLongPrefix(longPrefixIndex)
			if err != nil {
				b.img.putBlock(sblk)
				return fmt.Errorf("failed to get long prefix for shared xattr nid %d: %w", b.nid, err)
			}
		} else if xattrEntry.NameIndex != 0 {
			prefix = xattrIndex(xattrEntry.NameIndex).String()
		}

		if len(sb) < int(xattrEntry.NameLen)+int(xattrEntry.ValueLen) {
			b.img.putBlock(sblk)
			return fmt.Errorf("shared xattr too long for nid %d: %w", b.nid, ErrInvalid)
		}
		name := prefix + string(sb[:xattrEntry.NameLen])
		sb = sb[xattrEntry.NameLen:]
		stat.Xattrs[name] = string(sb[:xattrEntry.ValueLen])
		b.img.putBlock(sblk)

		xb = xb[4:]
	}

	pos := disk.SizeXattrBodyHeader + int(xh.SharedCount)*4
	reload := func() error {
		b.img.putBlock(blk)
		blk, err = b.img.loadAt(addr+int64(pos), int64(xsize-pos))
		if err != nil {
			return fmt.Errorf("failed to read xattr body for nid %d: %w", b.nid, err)
		}
		xb = blk.bytes()
		return nil
	}
	for pos < xsize {
		if len(xb) < disk.SizeXattrEntry {
			if err := reload(); err != nil {
				return err
			}
			if len(xb) < disk.SizeXattrEntry {
				return fmt.Errorf("xattr block too small for entry at pos %d for nid %d: %w", pos, b.nid, ErrInvalid)
			}
		}

		var xattrEntry disk.XattrEntry
		if _, err := binary.Decode(xb[:disk.SizeXattrEntry], binary.LittleEndian, &xattrEntry); err != nil {
			return err
		}
		pos += disk.SizeXattrEntry
		xb = xb[disk.SizeXattrEntry:]
		var prefix string
		if xattrEntry.NameIndex&0x80 == 0x80 {
			// Long prefix: highest bit set
			longPrefixIndex := xattrEntry.NameIndex & 0x7F
			var err error
			prefix, err = b.img.getLongPrefix(longPrefixIndex)
			if err != nil {
				return fmt.Errorf("failed to get long prefix for inline xattr nid %d: %w", b.nid, err)
			}
		} else if xattrEntry.NameIndex != 0 {
			prefix = xattrIndex(xattrEntry.NameIndex).String()
		}

		if len(xb) < int(xattrEntry.NameLen) {
			if err := reload(); err != nil {
				return err
			}
			if len(xb) < int(xattrEntry.NameLen) {
				return fmt.Errorf("xattr block too small for name of length %d for nid %d: %w", xattrEntry.NameLen, b.nid, ErrInvalid)
			}
		}
		name := prefix + string(xb[:xattrEntry.NameLen])
		pos += int(xattrEntry.NameLen)
		xb = xb[xattrEntry.NameLen:]

		var value string
		if len(xb) < int(xattrEntry.ValueLen) {
			remaining := int(xattrEntry.ValueLen)
			buf := make([]byte, 0, remaining)
			for remaining > 0 {
				copySize := len(xb)
				if copySize == 0 {
					if err := reload(); err != nil {
						return err
					}
					copySize = len(xb)
					if copySize == 0 {
						return fmt.Errorf("empty xattr block while reading value: %w", ErrInvalid)
					}
				}
				if remaining < copySize {
					copySize = remaining
				}
				buf = append(buf, xb[:copySize]...)
				remaining -= copySize
				pos += copySize
				xb = xb[copySize:]
			}
			value = string(buf)
		} else {
			value = string(xb[:xattrEntry.ValueLen])
			pos += int(xattrEntry.ValueLen)
			xb = xb[xattrEntry.ValueLen:]
		}
		stat.Xattrs[name] = value

		// Round up to next 4 byte boundary
		if rem := pos % 4; rem != 0 {
			pad := 4 - rem
			pos += pad
			if len(xb) < pad {
				xb = nil
			} else {
				xb = xb[pad:]
			}
		}
	}
	return nil
}
