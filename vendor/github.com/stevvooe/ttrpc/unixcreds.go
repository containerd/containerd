// +build linux freebsd solaris

package ttrpc

import (
	"context"
	"net"
	"os"
	"syscall"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

type UnixCredentialsFunc func(*unix.Ucred) error

func (fn UnixCredentialsFunc) Handshake(ctx context.Context, conn net.Conn) (net.Conn, interface{}, error) {
	uc, err := requireUnixSocket(conn)
	if err != nil {
		return nil, nil, errors.Wrap(err, "ttrpc.UnixCredentialsFunc: require unix socket")
	}

	// TODO(stevvooe): Calling (*UnixConn).File causes a 5x performance
	// decrease vs just accessing the fd directly. Need to do some more
	// troubleshooting to isolate this to Go runtime or kernel.
	fp, err := uc.File()
	if err != nil {
		return nil, nil, errors.Wrap(err, "ttrpc.UnixCredentialsFunc: failed to get unix file")
	}
	defer fp.Close() // this gets duped and must be closed when this method is complete.

	ucred, err := unix.GetsockoptUcred(int(fp.Fd()), unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "ttrpc.UnixCredentialsFunc: failed to retrieve socket peer credentials")
	}

	if err := fn(ucred); err != nil {
		return nil, nil, errors.Wrapf(err, "ttrpc.UnixCredentialsFunc: credential check failed")
	}

	return uc, ucred, nil
}

func UnixSocketRequireUidGid(uid, gid int) UnixCredentialsFunc {
	return func(ucred *unix.Ucred) error {
		return requireUidGid(ucred, uid, gid)
	}
}

func UnixSocketRequireRoot() UnixCredentialsFunc {
	return UnixSocketRequireUidGid(0, 0)
}

// UnixSocketRequireSameUser resolves the current unix user and returns a
// UnixCredentialsFunc that will validate incoming unix connections against the
// current credentials.
//
// This is useful when using abstract sockets that are accessible by all users.
func UnixSocketRequireSameUser() UnixCredentialsFunc {
	uid, gid := os.Getuid(), os.Getgid()
	return UnixSocketRequireUidGid(uid, gid)
}

func requireRoot(ucred *unix.Ucred) error {
	return requireUidGid(ucred, 0, 0)
}

func requireUidGid(ucred *unix.Ucred, uid, gid int) error {
	if (uid != -1 && uint32(uid) != ucred.Uid) || (gid != -1 && uint32(gid) != ucred.Gid) {
		return errors.Wrap(syscall.EPERM, "ttrpc: invalid credentials")
	}
	return nil
}

func requireUnixSocket(conn net.Conn) (*net.UnixConn, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, errors.New("a unix socket connection is required")
	}

	return uc, nil
}
