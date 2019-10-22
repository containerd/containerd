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
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// CgroupStats is per-cgroup statistics.
type CgroupStats struct {
	// number of tasks sleeping
	NrSleeping uint64
	// number of tasks running
	NrRunning uint64
	// number of tasks in stopped state
	NrStopped uint64
	// number of tasks in uninterruptible state
	NrUninterruptible uint64
	// number of tasks waiting on IO
	NrIoWait uint64
}

// TaskstatsClient handles communication with generic netlink taskstats
// family controller.
type TaskstatsClient struct {
	conn     *connection
	familyID uint16
}

// NewTaskstatsClient returns client for generic netlink taskstats family
// controller.
func NewTaskstatsClient() (*TaskstatsClient, error) {
	conn, err := newConnection()
	if err != nil {
		return nil, err
	}

	familyID, err := getFamilyID(conn, unix.TASKSTATS_GENL_NAME)
	if err != nil {
		conn.close()
		return nil, err
	}

	return &TaskstatsClient{
		conn:     conn,
		familyID: familyID,
	}, nil
}

// SetTimeout set timeout for send/receive.
func (cli *TaskstatsClient) SetTimeout(to time.Duration) error {
	return cli.conn.setTimeout(to)
}

// Close closes connection.
func (cli *TaskstatsClient) Close() error {
	return cli.conn.close()
}

// GetCgroupStats returns statistics for specific cgroup path.
func (cli *TaskstatsClient) GetCgroupStats(ctx context.Context, path string) (CgroupStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return CgroupStats{}, fmt.Errorf("genetlink: failed to open cgroup path %v: %v", path, err)
	}
	defer f.Close()

	// prepare command message
	buf := make([]byte, 4)
	getSysEndian().PutUint32(buf, uint32(f.Fd()))

	m := syscall.NetlinkMessage{
		Header: syscall.NlMsghdr{
			Type:  cli.familyID,
			Flags: syscall.NLM_F_REQUEST,
		},
		Data: (&GenlMsg{
			// no version for cgroupstats
			Header: GenlMsghdr{
				Command: unix.CGROUPSTATS_CMD_GET,
			},
			Data: (&Attribute{
				Typ:  unix.CGROUPSTATS_CMD_ATTR_FD,
				Data: buf,
			}).Marshal(),
		}).Marshal(),
	}

	msgs, err := cli.conn.executes(ctx, m)
	if err != nil {
		return CgroupStats{}, err
	}

	msg := msgs[0]
	return parseCGroupStatReply(msg)
}

func getFamilyID(conn *connection, name string) (uint16, error) {
	ctx := context.Background()

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
				Data: append([]byte(name), 0),
			}).Marshal(),
		}).Marshal(),
	}

	msgs, err := conn.executes(ctx, m)
	if err != nil {
		return 0, err
	}

	msg := msgs[0]
	got, err := parseFamilyReply(msg, unix.CTRL_ATTR_FAMILY_ID)
	if err != nil {
		return 0, err
	}
	return got.(uint16), nil
}

func parseFamilyReply(m syscall.NetlinkMessage, typ uint16) (interface{}, error) {
	gm := &GenlMsg{}
	if err := gm.Unmarshal(m.Data); err != nil {
		return 0, err
	}

	n := 0
	attr := &Attribute{}
	for n < len(gm.Data) {
		attr.Reset()
		if err := attr.Unmarshal(gm.Data[n:]); err != nil {
			return nil, err
		}

		n += attrAlign(int(attr.Len))
		if attr.Typ != typ {
			continue
		}

		switch attr.Typ {
		case unix.CTRL_ATTR_FAMILY_ID:
			return getSysEndian().Uint16(attr.Data[:2]), nil
		case unix.CTRL_ATTR_FAMILY_NAME:
			return string(attr.Data), nil
		default:
			break
		}
	}
	return nil, errors.New("genetlink: unsupport family attribute type or invalid message")
}

func parseCGroupStatReply(m syscall.NetlinkMessage) (CgroupStats, error) {
	gm := &GenlMsg{}
	if err := gm.Unmarshal(m.Data); err != nil {
		return CgroupStats{}, err
	}

	attr := &Attribute{}
	if err := attr.Unmarshal(gm.Data); err != nil {
		return CgroupStats{}, err
	}

	cs := CgroupStats{}
	if err := binary.Read(bytes.NewReader(attr.Data), getSysEndian(), &cs); err != nil {
		return CgroupStats{}, err
	}
	return cs, nil
}
