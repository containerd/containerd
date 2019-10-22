// +build linux

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package genetlink

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	// ErrInvalidErrorCode indicates the error code is invalid, like
	// missing bytes in message.
	ErrInvalidErrorCode = errors.New("genetlink: invalid error code in message")

	// ErrMismatchedSequence indicates the sequence in reply message must
	// be equal to request's sequence.
	ErrMismatchedSequence = errors.New("genetlink: mismatched sequence in message")

	// ErrMismatchedPortID indicates the port ID in reply message must
	// be equal to request's port ID.
	ErrMismatchedPortID = errors.New("genetlink: mismatched port ID in message")

	// ErrUnexpectedDoneMsg indicates that only receive DONE with empty
	// message.
	ErrUnexpectedDoneMsg = errors.New("genetlink: only receive DONE message")

	// ErrInvalidTimeout indicates acceptable timeout value >= 1 microsecond
	ErrInvalidTimeout = errors.New("genetlink: invalid timeout, min unit is microsecond")

	// ErrUnavailable indicates connection temporarily unavailable right now
	ErrUnavailable = errors.New("genetlink: connection is temporarily unavailable right now")
)

const (
	defaultTimeout = 10 * time.Second

	mutexUnlocked = 0
	mutexLocked   = 1
)

// connection represents a connection to generic netlink
type connection struct {
	locked int32

	sockFD int
	seq    uint32
	addr   syscall.SockaddrNetlink
	pid    uint32
}

func newConnection() (*connection, error) {
	sockFD, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_GENERIC)
	if err != nil {
		return nil, err
	}

	c := &connection{
		sockFD: sockFD,
		seq:    rand.Uint32(),
		pid:    uint32(os.Getpid()),
		addr: syscall.SockaddrNetlink{
			Family: syscall.AF_NETLINK,
		},
	}

	if err := syscall.Bind(c.sockFD, &c.addr); err != nil {
		syscall.Close(c.sockFD)
		return nil, err
	}
	return c, nil
}

func (c *connection) setTimeout(to time.Duration) error {
	if to < time.Microsecond {
		return ErrInvalidTimeout
	}

	tv := syscall.NsecToTimeval(to.Nanoseconds())
	if err := syscall.SetsockoptTimeval(c.sockFD, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		return err
	}

	// NOTE: Most of generic netlinks handles messages synchronously.
	// If there is any block issue in the generic netlink handler,
	// the Sendto syscall will hang until generic netlink handler return.
	return syscall.SetsockoptTimeval(c.sockFD, syscall.SOL_SOCKET, syscall.SO_SNDTIMEO, &tv)
}

// executes sends and receives message with concurrent safe.
func (c *connection) executes(ctx context.Context, m syscall.NetlinkMessage) ([]syscall.NetlinkMessage, error) {
	if err := c.tryLock(ctx); err != nil {
		return nil, err
	}
	defer c.unlock()

	reqM, err := c.sendMsg(m)
	if err != nil {
		return nil, err
	}
	return c.receive(reqM)
}

func (c *connection) close() error {
	return syscall.Close(c.sockFD)
}

func (c *connection) sendMsg(m syscall.NetlinkMessage) (syscall.NetlinkMessage, error) {
	m.Header.Seq, c.seq = c.seq, c.seq+1

	if m.Header.Pid == 0 {
		m.Header.Pid = c.pid
	}
	return m, syscall.Sendto(c.sockFD, marshalMessage(m), 0, &c.addr)
}

func (c *connection) receive(reqM syscall.NetlinkMessage) ([]syscall.NetlinkMessage, error) {
	res := make([]syscall.NetlinkMessage, 0)

	for {
		parts, err := c.recvMsgs()
		if err != nil {
			return nil, err
		}

		isDone := true
		for _, p := range parts {
			if err := c.validateMessage(reqM, p); err != nil {
				return nil, err
			}

			if p.Header.Flags&syscall.NLM_F_MULTI == 0 {
				continue
			}

			isDone = (p.Header.Type == syscall.NLMSG_DONE)
		}

		res = append(res, parts...)
		if isDone {
			break
		}
	}

	if len(res) > 1 && res[len(res)-1].Header.Type == syscall.NLMSG_DONE {
		res = res[:len(res)-1]
	}

	if len(res) == 1 && res[0].Header.Type == syscall.NLMSG_DONE {
		return nil, ErrUnexpectedDoneMsg
	}
	return res, nil
}

func (c *connection) recvMsgs() ([]syscall.NetlinkMessage, error) {
	b := make([]byte, sysPageSize)

	for {
		n, _, _, _, err := syscall.Recvmsg(c.sockFD, b, nil, syscall.MSG_PEEK)
		if err != nil {
			return nil, err
		}

		// need more bytes to do align if equal
		if n < len(b) {
			break
		}
		b = make([]byte, len(b)+sysPageSize)
	}

	n, _, _, _, err := syscall.Recvmsg(c.sockFD, b, nil, 0)
	if err != nil {
		return nil, err
	}

	n = msgAlign(n)
	return syscall.ParseNetlinkMessage(b[:n])
}

func (c *connection) validateMessage(reqM, m syscall.NetlinkMessage) error {
	if m.Header.Flags != syscall.NLMSG_ERROR {
		if m.Header.Seq != reqM.Header.Seq {
			return ErrMismatchedSequence
		}

		if m.Header.Pid != reqM.Header.Pid {
			return ErrMismatchedPortID
		}
		return nil
	}

	// from https://www.infradead.org/~tgr/libnl/doc/core.html#core_errmsg
	//
	// Error messages can be sent in response to a request. Error messages
	// must use the standard message type NLMSG_ERROR. The payload consists
	// of a error code and the original netlink mesage header of the
	// request.
	//
	// The error code will take 32 bits in the payload.
	if len(m.Data) < 4 {
		return ErrInvalidErrorCode
	}

	errCode := getSysEndian().Uint32(m.Data[:4])
	return fmt.Errorf("genetlink: receive error code %v", syscall.Errno(errCode))
}

// tryLock ensures that the coming request will not be blocked by previous hang
// request.
func (c *connection) tryLock(ctx context.Context) error {
	var (
		tctx   = ctx
		cancel context.CancelFunc
	)

	if _, ok := ctx.Deadline(); !ok {
		tctx, cancel = context.WithTimeout(tctx, defaultTimeout)
		defer cancel()
	}

	retry := 1
	for {
		locked := atomic.CompareAndSwapInt32(&c.locked, mutexUnlocked, mutexLocked)
		if locked {
			return nil
		}

		select {
		case <-time.After(time.Millisecond * time.Duration(rand.Intn(retry))):
			if retry < 1024 {
				retry = retry << 1
			}
			continue
		case <-tctx.Done():
			err := tctx.Err()

			if terr, ok := err.(interface {
				Timeout() bool
			}); ok && terr.Timeout() {
				return ErrUnavailable
			}
			return err
		}
	}
}

func (c *connection) unlock() {
	atomic.CompareAndSwapInt32(&c.locked, mutexLocked, mutexUnlocked)
}
