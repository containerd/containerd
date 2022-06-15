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

package cap

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const procPIDStatus = `Name:   cat
Umask:  0022
State:  R (running)
Tgid:   170065
Ngid:   0
Pid:    170065
PPid:   170064
TracerPid:      0
Uid:    0       0       0       0
Gid:    0       0       0       0
FDSize: 64
Groups: 0
NStgid: 170065
NSpid:  170065
NSpgid: 170064
NSsid:  3784
VmPeak:     8216 kB
VmSize:     8216 kB
VmLck:         0 kB
VmPin:         0 kB
VmHWM:       676 kB
VmRSS:       676 kB
RssAnon:              72 kB
RssFile:             604 kB
RssShmem:              0 kB
VmData:      324 kB
VmStk:       132 kB
VmExe:        20 kB
VmLib:      1612 kB
VmPTE:        56 kB
VmSwap:        0 kB
HugetlbPages:          0 kB
CoreDumping:    0
THP_enabled:    1
Threads:        1
SigQ:   0/63692
SigPnd: 0000000000000000
ShdPnd: 0000000000000000
SigBlk: 0000000000000000
SigIgn: 0000000000000000
SigCgt: 0000000000000000
CapInh: 0000000000000000
CapPrm: 000000ffffffffff
CapEff: 000000ffffffffff
CapBnd: 000000ffffffffff
CapAmb: 0000000000000000
NoNewPrivs:     0
Seccomp:        0
Speculation_Store_Bypass:       thread vulnerable
Cpus_allowed:   00000000,00000000,00000000,0000000f
Cpus_allowed_list:      0-3
Mems_allowed:   00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000000,00000001
Mems_allowed_list:      0
voluntary_ctxt_switches:        0
nonvoluntary_ctxt_switches:     0
`

func TestCapsList(t *testing.T) {
	assert.Len(t, caps316, 38)
	assert.Len(t, caps58, 40)
	assert.Len(t, caps59, 41)
}

func TestFromNumber(t *testing.T) {
	assert.Equal(t, "CAP_CHOWN", FromNumber(0))
	assert.Equal(t, "CAP_SYS_ADMIN", FromNumber(21))
	assert.Equal(t, "CAP_CHECKPOINT_RESTORE", FromNumber(40))
	assert.Equal(t, "", FromNumber(-1))
	assert.Equal(t, "", FromNumber(63))
	assert.Equal(t, "", FromNumber(255))
}

func TestFromBitmap(t *testing.T) {
	type testCase struct {
		comment    string
		v          uint64
		knownNames []string
		unknown    []int
	}
	testCases := []testCase{
		{
			comment: "No cap",
			v:       0x0000000000000000,
		},
		{
			// 3.10 (same caps as 3.5) is the oldest kernel version we want to support
			comment:    "All caps on kernel 3.5 (last = CAP_BLOCK_SUSPEND)",
			v:          0x0000001fffffffff,
			knownNames: caps35,
		},
		{
			comment:    "All caps on kernel 3.16 (last = CAP_AUDIT_READ)",
			v:          0x0000003fffffffff,
			knownNames: caps316,
		},
		{
			comment:    "All caps on kernel 5.8 (last = CAP_BPF)",
			v:          0x000000ffffffffff,
			knownNames: caps58,
		},
		{
			comment:    "All caps on kernel 5.9 (last = CAP_CHECKPOINT_RESTORE)",
			v:          0x000001ffffffffff,
			knownNames: caps59,
		},
		{
			comment:    "Unknown caps",
			v:          0xf00001ffffffffff,
			knownNames: caps59,
			unknown:    []int{60, 61, 62, 63},
		},
	}

	for _, tc := range testCases {
		knownNames, unknown := FromBitmap(tc.v)
		t.Logf("[%s] v=0x%x, got=%+v (%d entries), unknown=%v",
			tc.comment, tc.v, knownNames, len(knownNames), unknown)
		assert.Equal(t, tc.knownNames, knownNames)
		assert.Equal(t, tc.unknown, unknown)
	}
}

func TestParseProcPIDStatus(t *testing.T) {
	res, err := ParseProcPIDStatus(strings.NewReader(procPIDStatus))
	assert.NoError(t, err)
	expected := map[Type]uint64{
		Inheritable: 0,
		Permitted:   0xffffffffff,
		Effective:   0xffffffffff,
		Bounding:    0xffffffffff,
		Ambient:     0,
	}
	assert.EqualValues(t, expected, res)
}

func TestCurrent(t *testing.T) {
	caps, err := Current()
	assert.NoError(t, err)
	t.Logf("verify the result manually: %+v", caps)
}

func TestKnown(t *testing.T) {
	caps := Known()
	assert.EqualValues(t, caps59, caps)
}

func FuzzParseProcPIDStatus(f *testing.F) {
	f.Add(procPIDStatus)
	f.Fuzz(func(t *testing.T, s string) {
		result, err := ParseProcPIDStatus(bytes.NewReader([]byte(s)))
		if err != nil && result != nil {
			t.Errorf("either %+v or %+v must be nil", result, err)
		}
	})
}
