package fs

import (
	"syscall"

	"github.com/hanwen/go-fuse/v2/fuse"
)

// see rawBridge.setAttr
func (b *rawBridge) setStatx(out *fuse.Statx) {
	if !b.options.NullPermissions && out.Mode&07777 == 0 {
		out.Mode |= 0644
		if out.Mode&syscall.S_IFDIR != 0 {
			out.Mode |= 0111
		}
	}
	if b.options.UID != 0 && out.Uid == 0 {
		out.Uid = b.options.UID
	}
	if b.options.GID != 0 && out.Gid == 0 {
		out.Gid = b.options.GID
	}
	setStatxBlocks(out)
}

// see rawBridge.setAttrTimeout
func (b *rawBridge) setStatxTimeout(out *fuse.StatxOut) {
	if b.options.AttrTimeout != nil && out.Timeout() == 0 {
		out.SetTimeout(*b.options.AttrTimeout)
	}
}

func (b *rawBridge) Statx(cancel <-chan struct{}, in *fuse.StatxIn, out *fuse.StatxOut) fuse.Status {
	n, fe := b.inode(in.NodeId, in.Fh)
	var fh FileHandle
	if fe != nil {
		fh = fe.file
	}

	ctx := &fuse.Context{Caller: in.Caller, Cancel: cancel}

	errno := syscall.ENOSYS
	if sx, ok := n.ops.(NodeStatxer); ok {
		errno = sx.Statx(ctx, fh, in.SxFlags, in.SxMask, out)
	} else if fsx, ok := n.ops.(FileStatxer); ok {
		errno = fsx.Statx(ctx, in.SxFlags, in.SxMask, out)
	}

	if errno == 0 {
		if out.Ino != 0 && n.stableAttr.Ino > 1 && out.Ino != n.stableAttr.Ino {
			b.logf("warning: rawBridge.getattr: overriding ino %d with %d", out.Ino, n.stableAttr.Ino)
		}
		out.Ino = n.stableAttr.Ino
		out.Mode = (out.Statx.Mode & 07777) | uint16(n.stableAttr.Mode)
		b.setStatx(&out.Statx)
		b.setStatxTimeout(out)
	}

	return errnoToStatus(errno)
}
