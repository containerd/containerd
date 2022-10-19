# CHANGELOG

## v0.2.0

- [New API] [commit](https://github.com/mdlayher/socket/commit/6e912a68523c45e5fd899239f4b46c402dd856da):
  `socket.FileConn` can be used to create a `socket.Conn` from an existing
  `os.File`, which may be provided by systemd socket activation or another
  external mechanism.
- [API change] [commit](https://github.com/mdlayher/socket/commit/66d61f565188c23fe02b24099ddc856d538bf1a7):
  `socket.Conn.Connect` now returns the `unix.Sockaddr` value provided by
  `getpeername(2)`, since we have to invoke that system call anyway to verify
  that a connection to a remote peer was successfully established.
- [Bug Fix] [commit](https://github.com/mdlayher/socket/commit/b60b2dbe0ac3caff2338446a150083bde8c5c19c):
  check the correct error from `unix.GetsockoptInt` in the `socket.Conn.Connect`
  method. Thanks @vcabbage!

## v0.1.2

- [Bug Fix]: `socket.Conn.Connect` now properly checks the `SO_ERROR` socket
  option value after calling `connect(2)` to verify whether or not a connection
  could successfully be established. This means that `Connect` should now report
  an error for an `AF_INET` TCP connection refused or `AF_VSOCK` connection
  reset by peer.
- [New API]: add `socket.Conn.Getpeername` for use in `Connect`, but also for
  use by external callers.

## v0.1.1

- [New API]: `socket.Conn` now has `CloseRead`, `CloseWrite`, and `Shutdown`
  methods.
- [Improvement]: internal rework to more robustly handle various errors.

## v0.1.0

- Initial unstable release. Most functionality has been developed and ported
from package [`netlink`](https://github.com/mdlayher/netlink).
