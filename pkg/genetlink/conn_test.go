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
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestGetFamily(t *testing.T) {
	conn, err := newConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()

	target := "nlctrl"
	m := syscall.NetlinkMessage{
		Header: syscall.NlMsghdr{
			Type:  unix.GENL_ID_CTRL,
			Flags: syscall.NLM_F_REQUEST,
		},
		Data: (&GenlMsg{
			Header: GenlMsghdr{
				Command: unix.CTRL_CMD_GETFAMILY,
			},
			Data: (&Attribute{
				Typ:  unix.CTRL_ATTR_FAMILY_NAME,
				Data: append([]byte(target), 0),
			}).Marshal(),
		}).Marshal(),
	}

	ctx := context.Background()

	msgs, err := conn.executes(ctx, m)
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 reply message, but got %v", len(msgs))
	}

	name, err := parseFamilyReply(msgs[0], unix.CTRL_ATTR_FAMILY_NAME)
	if err != nil {
		t.Fatal(err)
	}

	got := strings.Trim(name.(string), "\x00")
	if strings.Compare(got, target) != 0 {
		t.Fatalf("expected name is %s, but got %s", target, got)
	}
}

func TestGetCgroupStats(t *testing.T) {
	cli, err := NewTaskstatsClient()
	if err != nil {
		t.Fatalf("failed to new client: %v", err)
	}
	defer cli.Close()

	var (
		ctx          = context.Background()
		cpuSubsystem = "/sys/fs/cgroup/cpu"
	)

	_, err = cli.GetCgroupStats(ctx, cpuSubsystem)
	if err != nil {
		t.Fatalf("failed to get cgroupstats for %s: %v", cpuSubsystem, err)
	}
	// TODO(fuweid): check fields here?
}

func TestVarifyTimeout(t *testing.T) {
	conn, err := newConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()

	var (
		expected = 1*time.Second + 100*time.Millisecond
		eTv      = unix.Timeval{Sec: 1, Usec: 100 * 1000}

		gotTv unix.Timeval
		v     = uint32(0x10)
	)

	if err := conn.setTimeout(expected); err != nil {
		t.Fatalf("setTimeout expected no error, but got %v", err)
	}

	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(conn.sockFD), unix.SOL_SOCKET, unix.SO_SNDTIMEO, uintptr(unsafe.Pointer(&gotTv)), uintptr(unsafe.Pointer(&v)), 0)
	if errno != 0 {
		t.Fatalf("failed to get send timeout option: %v", errno)
	}

	if gotTv.Sec != eTv.Sec || gotTv.Usec != eTv.Usec {
		t.Fatalf("expected send timeout %v, but got %v", eTv, gotTv)
	}

	_, _, errno = unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(conn.sockFD), unix.SOL_SOCKET, unix.SO_RCVTIMEO, uintptr(unsafe.Pointer(&gotTv)), uintptr(unsafe.Pointer(&v)), 0)
	if errno != 0 {
		t.Fatalf("failed to get receive timeout option: %v", errno)
	}

	if gotTv.Sec != eTv.Sec || gotTv.Usec != eTv.Usec {
		t.Fatalf("expected receive timeout %v, but got %v", eTv, gotTv)
	}
}

func TestConnTryLock(t *testing.T) {
	conn, err := newConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()

	ctx := context.Background()
	if err := conn.tryLock(ctx); err != nil {
		t.Fatalf("expected no error for first call to tryLock, but got %v", err)
	}

	tctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	err = conn.tryLock(tctx)
	cancel()
	if err != ErrUnavailable {
		t.Fatalf("expected timeout error to tryLock, but got %v", err)
	}

	tctx, cancel = context.WithCancel(ctx)
	cancel()
	if err := conn.tryLock(tctx); err != context.Canceled {
		t.Fatalf("expected canceled error to tryLock, but got %v", err)
	}

	conn.unlock()
	if err := conn.tryLock(ctx); err != nil {
		t.Fatalf("expected no error for tryLock to unlocked state, but got %v", err)
	}
}
