//go:build !windows

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

	syscall "golang.org/x/sys/unix"
)

const (
	local = 0
	peer  = 1
)

// SocketPair contains the file descriptors of a connected pair of sockets.
type SocketPair [2]int

// NewSocketPair returns a connected pair of sockets.
func NewSocketPair() (SocketPair, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return [2]int{-1, -1}, fmt.Errorf("failed to create socketpair: %w", err)
	}

	return fds, nil
}

// LocalFile returns the socketpair fd for local usage as an *os.File.
func (fds SocketPair) LocalFile() *os.File {
	return os.NewFile(uintptr(fds[local]), fds.fileName()+"[0]")
}

// PeerFile returns the socketpair fd for peer usage as an *os.File.
func (fds SocketPair) PeerFile() *os.File {
	return os.NewFile(uintptr(fds[peer]), fds.fileName()+"[1]")
}

// LocalConn returns a net.Conn for the local end of the socketpair.
func (fds SocketPair) LocalConn() (net.Conn, error) {
	file := fds.LocalFile()
	defer file.Close()
	conn, err := net.FileConn(file)
	if err != nil {
		return nil, fmt.Errorf("failed to create net.Conn for %s[0]: %w", fds.fileName(), err)
	}
	return conn, nil
}

// PeerConn returns a net.Conn for the peer end of the socketpair.
func (fds SocketPair) PeerConn() (net.Conn, error) {
	file := fds.PeerFile()
	defer file.Close()
	conn, err := net.FileConn(file)
	if err != nil {
		return nil, fmt.Errorf("failed to create net.Conn for %s[1]: %w", fds.fileName(), err)
	}
	return conn, nil
}

// Close closes both ends of the socketpair.
func (fds SocketPair) Close() {
	fds.LocalClose()
	fds.PeerClose()
}

// LocalClose closes the local end of the socketpair.
func (fds SocketPair) LocalClose() {
	syscall.Close(fds[local])
}

// PeerClose closes the peer end of the socketpair.
func (fds SocketPair) PeerClose() {
	syscall.Close(fds[peer])
}

func (fds SocketPair) fileName() string {
	return fmt.Sprintf("socketpair-#%d:%d[0]", fds[local], fds[peer])
}
