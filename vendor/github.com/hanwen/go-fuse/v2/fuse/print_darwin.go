// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"fmt"
)

func init() {
	initFlagNames.set(CAP_NODE_RWLOCK, "NODE_RWLOCK")
	initFlagNames.set(CAP_RENAME_SWAP, "RENAME_SWAP")
	initFlagNames.set(CAP_RENAME_EXCL, "RENAME_EXCL")
	initFlagNames.set(CAP_ALLOCATE, "ALLOCATE")
	initFlagNames.set(CAP_EXCHANGE_DATA, "EXCHANGE_DATA")
	initFlagNames.set(CAP_XTIMES, "XTIMES")
	initFlagNames.set(CAP_VOL_RENAME, "VOL_RENAME")
	initFlagNames.set(CAP_CASE_INSENSITIVE, "CASE_INSENSITIVE")
}

func (me *CreateIn) string() string {
	return fmt.Sprintf(
		"{0%o [%s]}", me.Mode,
		flagString(openFlagNames, int64(me.Flags), "O_RDONLY"))
}

func (me *GetAttrIn) string() string { return "" }

func (me *MknodIn) string() string {
	return fmt.Sprintf("{0%o, %d}", me.Mode, me.Rdev)
}

func (me *ReadIn) string() string {
	return fmt.Sprintf("{Fh %d [%d +%d) %s}",
		me.Fh, me.Offset, me.Size,
		flagString(readFlagNames, int64(me.ReadFlags), ""))
}

func (me *WriteIn) string() string {
	return fmt.Sprintf("{Fh %d [%d +%d) %s}",
		me.Fh, me.Offset, me.Size,
		flagString(writeFlagNames, int64(me.WriteFlags), ""))
}
