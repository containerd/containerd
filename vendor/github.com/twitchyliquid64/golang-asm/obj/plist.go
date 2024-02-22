// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package obj

import (
	"github.com/twitchyliquid64/golang-asm/objabi"
	"fmt"
	"strings"
)

type Plist struct {
	Firstpc *Prog
	Curfn   interface{} // holds a *gc.Node, if non-nil
}

// ProgAlloc is a function that allocates Progs.
// It is used to provide access to cached/bulk-allocated Progs to the assemblers.
type ProgAlloc func() *Prog

func Flushplist(ctxt *Link, plist *Plist, newprog ProgAlloc, myimportpath string) {
	// Build list of symbols, and assign instructions to lists.
	var curtext *LSym
	var etext *Prog
	var text []*LSym

	var plink *Prog
	for p := plist.Firstpc; p != nil; p = plink {
		if ctxt.Debugasm > 0 && ctxt.Debugvlog {
			fmt.Printf("obj: %v\n", p)
		}
		plink = p.Link
		p.Link = nil

		switch p.As {
		case AEND:
			continue

		case ATEXT:
			s := p.From.Sym
			if s == nil {
				// func _() { }
				curtext = nil
				continue
			}
			text = append(text, s)
			etext = p
			curtext = s
			continue

		case AFUNCDATA:
			// Rewrite reference to go_args_stackmap(SB) to the Go-provided declaration information.
			if curtext == nil { // func _() {}
				continue
			}
			if p.To.Sym.Name == "go_args_stackmap" {
				if p.From.Type != TYPE_CONST || p.From.Offset != objabi.FUNCDATA_ArgsPointerMaps {
					ctxt.Diag("FUNCDATA use of go_args_stackmap(SB) without FUNCDATA_ArgsPointerMaps")
				}
				p.To.Sym = ctxt.LookupDerived(curtext, curtext.Name+".args_stackmap")
			}

		}

		if curtext == nil {
			etext = nil
			continue
		}
		etext.Link = p
		etext = p
	}

	if newprog == nil {
		newprog = ctxt.NewProg
	}

	// Add reference to Go arguments for C or assembly functions without them.
	for _, s := range text {
		if !strings.HasPrefix(s.Name, "\"\".") {
			continue
		}
		found := false
		for p := s.Func.Text; p != nil; p = p.Link {
			if p.As == AFUNCDATA && p.From.Type == TYPE_CONST && p.From.Offset == objabi.FUNCDATA_ArgsPointerMaps {
				found = true
				break
			}
		}

		if !found {
			p := Appendp(s.Func.Text, newprog)
			p.As = AFUNCDATA
			p.From.Type = TYPE_CONST
			p.From.Offset = objabi.FUNCDATA_ArgsPointerMaps
			p.To.Type = TYPE_MEM
			p.To.Name = NAME_EXTERN
			p.To.Sym = ctxt.LookupDerived(s, s.Name+".args_stackmap")
		}
	}

	// Turn functions into machine code images.
	for _, s := range text {
		mkfwd(s)
		linkpatch(ctxt, s, newprog)
		ctxt.Arch.Preprocess(ctxt, s, newprog)
		ctxt.Arch.Assemble(ctxt, s, newprog)
		if ctxt.Errors > 0 {
			continue
		}
		linkpcln(ctxt, s)
		if myimportpath != "" {
			ctxt.populateDWARF(plist.Curfn, s, myimportpath)
		}
	}
}

func (ctxt *Link) InitTextSym(s *LSym, flag int) {
	if s == nil {
		// func _() { }
		return
	}
	if s.Func != nil {
		ctxt.Diag("InitTextSym double init for %s", s.Name)
	}
	s.Func = new(FuncInfo)
	if s.OnList() {
		ctxt.Diag("symbol %s listed multiple times", s.Name)
	}
	name := strings.Replace(s.Name, "\"\"", ctxt.Pkgpath, -1)
	s.Func.FuncID = objabi.GetFuncID(name, flag&WRAPPER != 0)
	s.Set(AttrOnList, true)
	s.Set(AttrDuplicateOK, flag&DUPOK != 0)
	s.Set(AttrNoSplit, flag&NOSPLIT != 0)
	s.Set(AttrReflectMethod, flag&REFLECTMETHOD != 0)
	s.Set(AttrWrapper, flag&WRAPPER != 0)
	s.Set(AttrNeedCtxt, flag&NEEDCTXT != 0)
	s.Set(AttrNoFrame, flag&NOFRAME != 0)
	s.Set(AttrTopFrame, flag&TOPFRAME != 0)
	s.Type = objabi.STEXT
	ctxt.Text = append(ctxt.Text, s)

	// Set up DWARF entries for s
	ctxt.dwarfSym(s)
}

func (ctxt *Link) Globl(s *LSym, size int64, flag int) {
	if s.OnList() {
		ctxt.Diag("symbol %s listed multiple times", s.Name)
	}
	s.Set(AttrOnList, true)
	ctxt.Data = append(ctxt.Data, s)
	s.Size = size
	if s.Type == 0 {
		s.Type = objabi.SBSS
	}
	if flag&DUPOK != 0 {
		s.Set(AttrDuplicateOK, true)
	}
	if flag&RODATA != 0 {
		s.Type = objabi.SRODATA
	} else if flag&NOPTR != 0 {
		if s.Type == objabi.SDATA {
			s.Type = objabi.SNOPTRDATA
		} else {
			s.Type = objabi.SNOPTRBSS
		}
	} else if flag&TLSBSS != 0 {
		s.Type = objabi.STLSBSS
	}
	if strings.HasPrefix(s.Name, "\"\"."+StaticNamePref) {
		s.Set(AttrStatic, true)
	}
}

// EmitEntryLiveness generates PCDATA Progs after p to switch to the
// liveness map active at the entry of function s. It returns the last
// Prog generated.
func (ctxt *Link) EmitEntryLiveness(s *LSym, p *Prog, newprog ProgAlloc) *Prog {
	pcdata := ctxt.EmitEntryStackMap(s, p, newprog)
	pcdata = ctxt.EmitEntryRegMap(s, pcdata, newprog)
	return pcdata
}

// Similar to EmitEntryLiveness, but just emit stack map.
func (ctxt *Link) EmitEntryStackMap(s *LSym, p *Prog, newprog ProgAlloc) *Prog {
	pcdata := Appendp(p, newprog)
	pcdata.Pos = s.Func.Text.Pos
	pcdata.As = APCDATA
	pcdata.From.Type = TYPE_CONST
	pcdata.From.Offset = objabi.PCDATA_StackMapIndex
	pcdata.To.Type = TYPE_CONST
	pcdata.To.Offset = -1 // pcdata starts at -1 at function entry

	return pcdata
}

// Similar to EmitEntryLiveness, but just emit register map.
func (ctxt *Link) EmitEntryRegMap(s *LSym, p *Prog, newprog ProgAlloc) *Prog {
	pcdata := Appendp(p, newprog)
	pcdata.Pos = s.Func.Text.Pos
	pcdata.As = APCDATA
	pcdata.From.Type = TYPE_CONST
	pcdata.From.Offset = objabi.PCDATA_RegMapIndex
	pcdata.To.Type = TYPE_CONST
	pcdata.To.Offset = -1

	return pcdata
}

// StartUnsafePoint generates PCDATA Progs after p to mark the
// beginning of an unsafe point. The unsafe point starts immediately
// after p.
// It returns the last Prog generated.
func (ctxt *Link) StartUnsafePoint(p *Prog, newprog ProgAlloc) *Prog {
	pcdata := Appendp(p, newprog)
	pcdata.As = APCDATA
	pcdata.From.Type = TYPE_CONST
	pcdata.From.Offset = objabi.PCDATA_RegMapIndex
	pcdata.To.Type = TYPE_CONST
	pcdata.To.Offset = objabi.PCDATA_RegMapUnsafe

	return pcdata
}

// EndUnsafePoint generates PCDATA Progs after p to mark the end of an
// unsafe point, restoring the register map index to oldval.
// The unsafe point ends right after p.
// It returns the last Prog generated.
func (ctxt *Link) EndUnsafePoint(p *Prog, newprog ProgAlloc, oldval int64) *Prog {
	pcdata := Appendp(p, newprog)
	pcdata.As = APCDATA
	pcdata.From.Type = TYPE_CONST
	pcdata.From.Offset = objabi.PCDATA_RegMapIndex
	pcdata.To.Type = TYPE_CONST
	pcdata.To.Offset = oldval

	return pcdata
}

// MarkUnsafePoints inserts PCDATAs to mark nonpreemptible and restartable
// instruction sequences, based on isUnsafePoint and isRestartable predicate.
// p0 is the start of the instruction stream.
// isUnsafePoint(p) returns true if p is not safe for async preemption.
// isRestartable(p) returns true if we can restart at the start of p (this Prog)
// upon async preemption. (Currently multi-Prog restartable sequence is not
// supported.)
// isRestartable can be nil. In this case it is treated as always returning false.
// If isUnsafePoint(p) and isRestartable(p) are both true, it is treated as
// an unsafe point.
func MarkUnsafePoints(ctxt *Link, p0 *Prog, newprog ProgAlloc, isUnsafePoint, isRestartable func(*Prog) bool) {
	if isRestartable == nil {
		// Default implementation: nothing is restartable.
		isRestartable = func(*Prog) bool { return false }
	}
	prev := p0
	prevPcdata := int64(-1) // entry PC data value
	prevRestart := int64(0)
	for p := prev.Link; p != nil; p, prev = p.Link, p {
		if p.As == APCDATA && p.From.Offset == objabi.PCDATA_RegMapIndex {
			prevPcdata = p.To.Offset
			continue
		}
		if prevPcdata == objabi.PCDATA_RegMapUnsafe {
			continue // already unsafe
		}
		if isUnsafePoint(p) {
			q := ctxt.StartUnsafePoint(prev, newprog)
			q.Pc = p.Pc
			q.Link = p
			// Advance to the end of unsafe point.
			for p.Link != nil && isUnsafePoint(p.Link) {
				p = p.Link
			}
			if p.Link == nil {
				break // Reached the end, don't bother marking the end
			}
			p = ctxt.EndUnsafePoint(p, newprog, prevPcdata)
			p.Pc = p.Link.Pc
			continue
		}
		if isRestartable(p) {
			val := int64(objabi.PCDATA_Restart1)
			if val == prevRestart {
				val = objabi.PCDATA_Restart2
			}
			prevRestart = val
			q := Appendp(prev, newprog)
			q.As = APCDATA
			q.From.Type = TYPE_CONST
			q.From.Offset = objabi.PCDATA_RegMapIndex
			q.To.Type = TYPE_CONST
			q.To.Offset = val
			q.Pc = p.Pc
			q.Link = p

			if p.Link == nil {
				break // Reached the end, don't bother marking the end
			}
			if isRestartable(p.Link) {
				// Next Prog is also restartable. No need to mark the end
				// of this sequence. We'll just go ahead mark the next one.
				continue
			}
			p = Appendp(p, newprog)
			p.As = APCDATA
			p.From.Type = TYPE_CONST
			p.From.Offset = objabi.PCDATA_RegMapIndex
			p.To.Type = TYPE_CONST
			p.To.Offset = prevPcdata
			p.Pc = p.Link.Pc
		}
	}
}
