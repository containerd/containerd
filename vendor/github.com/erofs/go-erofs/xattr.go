package erofs

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"

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

func setXattrs(b *file, addr int64, blk *block) (err error) {
	b.info.stat.Xattrs = map[string]string{}
	blkSize := int32(1 << b.img.sb.BlkSizeBits)

	blk.offset = int32(addr & int64(blkSize-1))
	if blk.end != blkSize || blk.end-blk.offset < disk.SizeXattrBodyHeader {
		b.img.putBlock(blk)
		blk, err = b.img.loadAt(addr, int64(b.info.xsize))
		if err != nil {
			return fmt.Errorf("failed to read xattr body for nid %d: %w", b.inode, err)
		}
	}
	var (
		xb = blk.bytes()
		xh disk.XattrHeader
	)
	if _, err := binary.Decode(xb[:disk.SizeXattrBodyHeader], binary.LittleEndian, &xh); err != nil {
		return err
	}
	xb = xb[disk.SizeXattrBodyHeader:]

	for i := 0; i < int(xh.SharedCount); i++ {
		if len(xb) < 4 {
			pos := disk.SizeXattrBodyHeader + int64(i)*4
			b.img.putBlock(blk)
			blk, err = b.img.loadAt(addr+pos, int64(b.info.xsize)-pos)
			if err != nil {
				return fmt.Errorf("failed to read xattr body for nid %d: %w", b.inode, err)
			}
			xb = blk.bytes()
		}
		var xattrAddr uint32
		if _, err := binary.Decode(xb[:4], binary.LittleEndian, &xattrAddr); err != nil {
			return err
		}

		// TODO: Cache shared xattr blocks
		blk, err := b.img.loadAt(int64(b.img.sb.XattrBlkAddr)<<b.img.sb.BlkSizeBits+int64(xattrAddr*4), int64(blkSize))
		if err != nil {
			return fmt.Errorf("failed to read shared xattr body for nid %d: %w", b.inode, err)
		}
		sb := blk.bytes()
		var xattrEntry disk.XattrEntry
		if _, err := binary.Decode(sb[:disk.SizeXattrEntry], binary.LittleEndian, &xattrEntry); err != nil {
			return err
		}
		sb = sb[disk.SizeXattrEntry:]
		var prefix string
		if xattrEntry.NameIndex&0x80 == 0x80 {
			//nameIndex := xattrEntry.NameIndex & 0x7F
			// TODO: Get long prefix
			return fmt.Errorf("shared xattr with long prefix not implemented for nid %d", b.inode)
		} else if xattrEntry.NameIndex != 0 {
			prefix = xattrIndex(xattrEntry.NameIndex).String()
		}

		if len(sb) < int(xattrEntry.NameLen)+int(xattrEntry.ValueLen) {
			return fmt.Errorf("shared xattr too long for inode %d", b.inode)
		}
		name := prefix + string(sb[:xattrEntry.NameLen])
		sb = sb[xattrEntry.NameLen:]
		b.info.stat.Xattrs[name] = string(sb[:xattrEntry.ValueLen])

		xb = xb[4:]
	}

	pos := disk.SizeXattrBodyHeader + int(xh.SharedCount)*4
	reload := func() error {
		b.img.putBlock(blk)
		blk, err = b.img.loadAt(addr+int64(pos), int64(b.info.xsize-pos))
		if err != nil {
			return fmt.Errorf("failed to read xattr body for nid %d: %w", b.inode, err)
		}
		xb = blk.bytes()
		return nil
	}
	for pos < b.info.xsize {
		if len(xb) < disk.SizeXattrEntry {
			if err := reload(); err != nil {
				return err
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
			//nameIndex := xattrEntry.NameIndex & 0x7F
			// TODO: Get long prefix
			return fmt.Errorf("shared xattr with long prefix not implemented for nid %d", b.inode)
		} else if xattrEntry.NameIndex != 0 {
			prefix = xattrIndex(xattrEntry.NameIndex).String()
		}

		if len(xb) < int(xattrEntry.NameLen) {
			if err := reload(); err != nil {
				return err
			}
		}
		name := prefix + string(xb[:xattrEntry.NameLen])
		pos += int(xattrEntry.NameLen)
		xb = xb[xattrEntry.NameLen:]

		var value string
		if len(xb) < int(xattrEntry.ValueLen) {
			remaining := int(xattrEntry.ValueLen)
			var b strings.Builder
			for remaining > 0 {
				copySize := len(xb)
				if copySize == 0 {
					if err := reload(); err != nil {
						return err
					}
					copySize = len(xb)
				}
				if remaining < copySize {
					copySize = remaining
				}
				n, err := b.Write(xb[:copySize])
				if err != nil {
					return err
				} else if n != copySize {
					return io.ErrShortWrite
				}
				remaining -= n
				pos += n
				xb = xb[copySize:]
			}
			value = b.String()
		} else {
			value = string(xb[:xattrEntry.ValueLen])
			pos += int(xattrEntry.ValueLen)
			xb = xb[xattrEntry.ValueLen:]
		}
		b.info.stat.Xattrs[name] = value

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
