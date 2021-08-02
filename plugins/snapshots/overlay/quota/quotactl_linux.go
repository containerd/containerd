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

package quota

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	DQUOT_VERSION = 0x1 //nolint

	FS_DQ_BHARD = 0x8 //nolint
	FS_DQ_BSOFT = 0x4 //nolint
)

// type
type quotaType uint32

const (
	XFS_USER_QUOTA  quotaType = (1 << iota) //nolint
	XFS_PROJ_QUOTA  quotaType = (1 << iota) //nolint
	XFS_GROUP_QUOTA quotaType = (1 << iota) //nolint
)

// command
type quotaCmd uint32

const (
	Q_XQUOTAON  quotaCmd = iota + 1 /* enable accounting/enforcement */  //nolint
	Q_XQUOTAOFF                     /* disable accounting/enforcement */ //nolint
	Q_XGETQUOTA                     /* get disk limits and usage */      //nolint
	Q_XSETQLIM                      /* set disk limits */                //nolint
	Q_XGETQSTAT                     /* get quota subsystem status */     //nolint
	Q_XQUOTARM                      /* free disk space used by dquots */ //nolint
)

// reference: https://man7.org/linux/man-pages/man2/quotactl.2.html
//
//nolint:structcheck
type fsDiskQuota struct { //nolint
	version      int8   /* Version of this structure */
	flags        int8   /* XFS_{USER,PROJ,GROUP}_QUOTA */
	fieldmask    uint16 /* Field specifier */
	id           uint32 /* User, project, or group ID */
	blkHardLimit uint64 /* Absolute limit on disk blocks */
	blkSoftLimit uint64 /* Preferred limit on disk blocks */
	inoHardLimit uint64 /* Maximum # allocated inodes */
	inoSoftLimit uint64 /* Preferred inode limit */
	bcount       uint64 /* # disk blocks owned by the user */
	icount       uint64 /* # inodes owned by the user */
	itimer       int32  /* Zero if within inode limits, If not, we refuse service */
	btimer       int32  /* Similar to above; for disk blocks */

	iwarns       uint16   /* # warnings issued with respect to # of inodes */
	bwarns       uint16   /* # warnings issued with respect to disk blocks */
	padding2     int32    /* Padding - for future use */
	rtbHardLimit uint64   /* Absolute limit on realtime (RT) disk blocks */
	rtbSoftLimit uint64   /* Preferred limit on RT disk blocks */
	rtdcount     uint64   /* # realtime blocks owned */
	rtbtimer     int32    /* Similar to above; for RT disk blocks */
	rtbwarns     uint16   /* # warnings issued with respect to RT disk blocks */
	padding3     int16    /* Padding - for future use */
	padding4     [4]uint8 /* Yet more padding */
}

func qcmd(cmd quotaCmd, qt quotaType) uint32 {
	return (uint32(xNumber)<<8+uint32(cmd))<<8 + uint32(qt)
}

func SetProjectQuota(backingFsBlockDev string, projectID uint32, quota Size) error {
	var fd = fsDiskQuota{}
	fd.version = DQUOT_VERSION
	fd.id = projectID
	fd.flags = int8(XFS_PROJ_QUOTA)

	fd.fieldmask = FS_DQ_BHARD | FS_DQ_BSOFT
	fd.blkHardLimit = uint64(quota) / 512
	fd.blkSoftLimit = fd.blkHardLimit
	devbyte := append([]byte(backingFsBlockDev), 0)

	_, _, errno := syscall.Syscall6(syscall.SYS_QUOTACTL, uintptr(qcmd(Q_XSETQLIM, XFS_PROJ_QUOTA)),
		uintptr(unsafe.Pointer(&devbyte[0])), uintptr(fd.id),
		uintptr(unsafe.Pointer(&fd)), 0, 0)
	if errno != 0 {
		return fmt.Errorf("failed to set quota limit for projid %d on %s: %v",
			projectID, backingFsBlockDev, errno.Error())
	}
	return nil
}

func GetProjectQuota(backingFsBlockDev string, projectID uint32) (Size, error) {
	var fd = fsDiskQuota{}
	devbyte := append([]byte(backingFsBlockDev), 0)

	_, _, errno := syscall.Syscall6(syscall.SYS_QUOTACTL, uintptr(qcmd(Q_XGETQUOTA, XFS_PROJ_QUOTA)),
		uintptr(unsafe.Pointer(&devbyte[0])), uintptr(projectID),
		uintptr(unsafe.Pointer(&fd)), 0, 0)
	if errno != 0 {
		return 0, fmt.Errorf("failed to get quota limit for projid %d on %s: %v",
			projectID, backingFsBlockDev, errno.Error())
	}
	return Size(fd.blkHardLimit) * 512, nil
}
