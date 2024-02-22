// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package riscv

import (
	"fmt"

	"github.com/twitchyliquid64/golang-asm/obj"
)

func init() {
	obj.RegisterRegister(obj.RBaseRISCV, REG_END, RegName)
	obj.RegisterOpcode(obj.ABaseRISCV, Anames)
}

func RegName(r int) string {
	switch {
	case r == 0:
		return "NONE"
	case r == REG_G:
		return "g"
	case r == REG_SP:
		return "SP"
	case REG_X0 <= r && r <= REG_X31:
		return fmt.Sprintf("X%d", r-REG_X0)
	case REG_F0 <= r && r <= REG_F31:
		return fmt.Sprintf("F%d", r-REG_F0)
	default:
		return fmt.Sprintf("Rgok(%d)", r-obj.RBaseRISCV)
	}
}
