//go:build windows

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

package net

import (
	"fmt"
	"net"
	"os"
	"unsafe"

	sys "golang.org/x/sys/windows"
)

// SocketPair contains a connected pair of sockets.
type SocketPair [2]sys.Handle

const (
	local = 0
	peer  = 1
)

// NewSocketPair returns a connected pair of sockets.
func NewSocketPair() (SocketPair, error) {
	/*	return [2]sys.Handle{sys.InvalidHandle, sys.InvalidHandle},
		errors.New("failed to emulatesocketpair, unimplemented for windows")*/

	// untested: return emulateWithPreConnect()
	return emulateWithPreConnect()
}

func emulateWithPreConnect() (SocketPair, error) {
	var (
		invalid = SocketPair{sys.InvalidHandle, sys.InvalidHandle}
		sa      sys.SockaddrInet4
		//sn      sys.Sockaddr
		l   sys.Handle
		a   sys.Handle
		p   sys.Handle
		err error
	)

	l, err = socket(sys.AF_INET, sys.SOCK_STREAM, 0)
	if err != nil {
		return invalid, fmt.Errorf("failed to emulate socketpair (local Socket()): %w", err)
	}
	defer func() {
		if err != nil {
			sys.CloseHandle(l)
		}
	}()

	sa.Addr[0] = 127
	sa.Addr[3] = 1
	sa.Port = 9999

	err = sys.Bind(l, &sa)
	if err != nil {
		return invalid, fmt.Errorf("failed to emulate socketpair (Bind()): %w", err)
	}

	/*sn, err = sys.Getsockname(l)
	if err != nil {
		return invalid, fmt.Errorf("failed to emulate socketpair (Getsockname()): %w", err)
	}*/

	p, err = socket(sys.AF_INET, sys.SOCK_STREAM, 0)
	if err != nil {
		return invalid, fmt.Errorf("failed to emulate socketpair (peer Socket()): %w", err)
	}

	defer func() {
		if err != nil {
			sys.CloseHandle(p)
		}
	}()

	err = sys.Listen(l, 0)
	if err != nil {
		return invalid, fmt.Errorf("failed to emulate socketpair (Listen()): %w", err)
	}

	go func() {
		err = connect(p, &sa)
	}()

	a, err = accept(l, sys.AF_INET, sys.SOCK_STREAM, 0)
	if err != nil {
		return invalid, fmt.Errorf("failed to emualte socketpair (Accept()): %w", err)
	}
	defer func() {
		if err != nil {
			sys.CloseHandle(a)
		}
	}()

	sys.CloseHandle(l)
	return SocketPair{a, p}, nil
}

// Close closes both ends of the socketpair.
func (sp SocketPair) Close() {
	sp.LocalClose()
	sp.PeerClose()
}

// LocalFile returns the socketpair fd for local usage as an *os.File.
func (sp SocketPair) LocalFile() *os.File {
	return os.NewFile(uintptr(sp[local]), sp.fileName()+"[0]")
}

// PeerFile returns the socketpair fd for peer usage as an *os.File.
func (sp SocketPair) PeerFile() *os.File {
	return os.NewFile(uintptr(sp[peer]), sp.fileName()+"[1]")
}

// LocalConn returns a net.Conn for the local end of the socketpair.
func (sp SocketPair) LocalConn() (net.Conn, error) {
	file := sp.LocalFile()
	defer file.Close()
	conn, err := net.FileConn(file)
	if err != nil {
		return nil, fmt.Errorf("failed to create net.Conn for %s[0]: %w", sp.fileName(), err)
	}
	return conn, nil
}

// PeerConn returns a net.Conn for the peer end of the socketpair.
func (sp SocketPair) PeerConn() (net.Conn, error) {
	file := sp.PeerFile()
	defer file.Close()
	conn, err := net.FileConn(file)
	if err != nil {
		return nil, fmt.Errorf("failed to create net.Conn for %s[1]: %w", sp.fileName(), err)
	}
	return conn, nil
}

// LocalClose closes the local end of the socketpair.
func (sp SocketPair) LocalClose() {
	sys.CloseHandle(sp[local])
}

// PeerClose closes the peer end of the socketpair.
func (sp SocketPair) PeerClose() {
	sys.CloseHandle(sp[peer])
}

func (sp SocketPair) fileName() string {
	return fmt.Sprintf("socketpair-#%d:%d[0]", sp[local], sp[peer])
}

func socket(domain, typ, proto int) (sys.Handle, error) {
	return sys.WSASocket(int32(domain), int32(typ), int32(proto), nil, 0, sys.WSA_FLAG_OVERLAPPED)
}

func connect(s sys.Handle, sa sys.Sockaddr) error {
	o := &sys.Overlapped{}
	return sys.ConnectEx(s, sa, nil, 0, nil, o)
}

func accept(l sys.Handle, domain, typ, proto int) (sys.Handle, error) {
	var (
		a       sys.Handle
		err     error
		buf     = [1024]byte{}
		overlap = sys.Overlapped{}
		cnt     = uint32(16 + 256)
	)

	a, err = socket(sys.AF_INET, sys.SOCK_STREAM, 0)
	if err != nil {
		return sys.InvalidHandle, fmt.Errorf("failed to emulate socketpair (accept): %w", err)
	}

	err = sys.AcceptEx(l, a, (*byte)(unsafe.Pointer(&buf)), 0, cnt, cnt, &cnt, &overlap)
	if err != nil {
		return sys.InvalidHandle, fmt.Errorf("failed to emulate socketpair (AcceptEx()): %w", err)
	}

	return a, nil
}
