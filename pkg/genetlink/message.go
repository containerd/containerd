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
	"errors"
	"syscall"
)

var (
	nlaHdrLen   = syscall.NLA_HDRLEN
	nlmsgHdrLen = syscall.NLMSG_HDRLEN

	sizeOfGenlMsgHdr = 4

	// ErrInvalidGenlMsg indicates that failed to unmarshal data into
	// GenlMsg with short length.
	ErrInvalidGenlMsg = errors.New("genetlink: invalid genl message")

	// ErrInvalidAttr indicates that failed to unmarshal data into Attribute
	// with short length or too large.
	ErrInvalidAttr = errors.New("genetlink: invalid attribute with mismatched length")
)

// GenlMsghdr represents genetlink header from include/uapi/linux/genetlink.h.
type GenlMsghdr struct {
	Command  uint8
	Version  uint8
	Reserved uint16
}

// GenlMsg is one kind of payload for generic netlink.
type GenlMsg struct {
	Header GenlMsghdr
	Data   []byte
}

// Marshal marshals GenlMsg into binary.
func (g *GenlMsg) Marshal() []byte {
	buf := make([]byte, sizeOfGenlMsgHdr+len(g.Data))

	buf[0] = g.Header.Command
	buf[1] = g.Header.Version
	// unused for buf[2:4]
	copy(buf[sizeOfGenlMsgHdr:], g.Data)
	return buf
}

// Unmarshal unpacks binary into GenlMsg.
func (g *GenlMsg) Unmarshal(buf []byte) error {
	if len(buf) < sizeOfGenlMsgHdr {
		return ErrInvalidGenlMsg
	}

	g.Header.Command = buf[0]
	g.Header.Version = buf[1]

	data := make([]byte, len(buf)-sizeOfGenlMsgHdr)
	copy(data, buf[sizeOfGenlMsgHdr:])
	g.Data = data
	return nil
}

// Attribute extends syscall.RtAttr with data reference
type Attribute struct {
	Len  uint16
	Typ  uint16
	Data []byte
}

// Reset emptys all fields.
func (a *Attribute) Reset() {
	a.Len = 0
	a.Typ = 0
	a.Data = nil
}

// Marshal marshals Attribute into binary.
func (a *Attribute) Marshal() []byte {
	order := getSysEndian()

	a.Len = uint16(nlaHdrLen + len(a.Data))
	n := attrAlign(int(a.Len))

	buf := make([]byte, n)
	order.PutUint16(buf[:2], a.Len)
	order.PutUint16(buf[2:4], a.Typ)

	// NOTE: nlaHdrLen is always larger than syscall.RtAttr.
	copy(buf[nlaHdrLen:], a.Data)
	return buf
}

// Unmarshal unpacks binary into Attribute.
func (a *Attribute) Unmarshal(buf []byte) error {
	if len(buf) < syscall.NLA_HDRLEN {
		return ErrInvalidAttr
	}

	order := getSysEndian()

	a.Len = order.Uint16(buf[:2])
	a.Typ = order.Uint16(buf[2:4])
	if int(a.Len) > len(buf) {
		return ErrInvalidAttr
	}

	data := make([]byte, int(a.Len)-nlaHdrLen)
	copy(data, buf[nlaHdrLen:a.Len])
	a.Data = data
	return nil
}

func marshalMessage(m syscall.NetlinkMessage) []byte {
	m.Header.Len = uint32(nlmsgHdrLen + len(m.Data))
	n := msgAlign(int(m.Header.Len))

	buf := make([]byte, n)

	order := getSysEndian()
	order.PutUint32(buf[:4], m.Header.Len)
	order.PutUint16(buf[4:6], m.Header.Type)
	order.PutUint16(buf[6:8], m.Header.Flags)
	order.PutUint32(buf[8:12], m.Header.Seq)
	order.PutUint32(buf[12:16], m.Header.Pid)

	// NOTE: nlmsgHdrLen is always larger than syscall.SizeofNlMsghdr
	copy(buf[nlmsgHdrLen:], m.Data)
	return buf
}

func attrAlign(n int) int {
	return (n + syscall.NLA_ALIGNTO - 1) & ^(syscall.NLA_ALIGNTO - 1)
}

func msgAlign(n int) int {
	return (n + syscall.NLMSG_ALIGNTO - 1) & ^(syscall.NLMSG_ALIGNTO - 1)
}
