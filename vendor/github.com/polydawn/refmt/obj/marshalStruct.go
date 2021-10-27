package obj

import (
	"fmt"
	"reflect"

	"github.com/polydawn/refmt/obj/atlas"
	. "github.com/polydawn/refmt/tok"
)

type marshalMachineStructAtlas struct {
	cfg *atlas.AtlasEntry // set on initialization

	target_rv reflect.Value
	index     int           // Progress marker
	value_rv  reflect.Value // Next value (or nil if next step is key).
}

func (mach *marshalMachineStructAtlas) Reset(slab *marshalSlab, rv reflect.Value, _ reflect.Type) error {
	mach.target_rv = rv
	mach.index = -1
	mach.value_rv = reflect.Value{}
	slab.grow() // we'll reuse the same row for all fields
	return nil
}

func (mach *marshalMachineStructAtlas) Step(driver *Marshaller, slab *marshalSlab, tok *Token) (done bool, err error) {
	//fmt.Printf("--step on %#v: i=%d/%d v=%v\n", mach.target_rv, mach.index, len(mach.cfg.Fields), mach.value)

	// Check boundaries and do the special steps or either start or end.
	nEntries := len(mach.cfg.StructMap.Fields)
	if mach.index < 0 {
		tok.Type = TMapOpen
		tok.Length = countEmittableStructFields(mach.cfg, mach.target_rv)
		tok.Tagged = mach.cfg.Tagged
		tok.Tag = mach.cfg.Tag
		mach.index++
		return false, nil
	}
	if mach.index == nEntries {
		tok.Type = TMapClose
		mach.index++
		slab.release()
		return true, nil
	}
	if mach.index > nEntries {
		return true, fmt.Errorf("invalid state: entire struct (%d fields) already consumed", nEntries)
	}

	// If value loaded from last step, recurse into handling that.
	fieldEntry := mach.cfg.StructMap.Fields[mach.index]
	if mach.value_rv != (reflect.Value{}) {
		child_rv := mach.value_rv
		mach.index++
		mach.value_rv = reflect.Value{}
		return false, driver.Recurse(
			tok,
			child_rv,
			fieldEntry.Type,
			slab.yieldMachine(fieldEntry.Type),
		)
	}

	// If value was nil, that indicates we're supposed to pick the value and yield a key.
	//  We have to look ahead to the value because if it's zero and tagged as
	//  omitEmpty, then we have to skip emitting the key as well.
	for fieldEntry.Ignore {
		mach.index++
		if mach.index == nEntries {
			tok.Type = TMapClose
			mach.index++
			slab.release()
			return true, nil
		}
		fieldEntry = mach.cfg.StructMap.Fields[mach.index]
	}
	mach.value_rv = fieldEntry.ReflectRoute.TraverseToValue(mach.target_rv)
	if fieldEntry.OmitEmpty && isEmptyValue(mach.value_rv) {
		mach.value_rv = reflect.Value{}
		mach.index++
		return mach.Step(driver, slab, tok)
	}
	tok.Type = TString
	tok.Str = fieldEntry.SerialName
	return false, nil
}

// Count how many fields in a struct should actually be marshalled.
// Fields that are tagged omitEmpty and are isEmptyValue are not counted, and
// StructMapEntry used to flag ignored fields unmarshalling never count, so
// this number may be less than the number of fields in the AtlasEntry.StructMap.
func countEmittableStructFields(cfg *atlas.AtlasEntry, target_rv reflect.Value) int {
	total := 0
	for _, fieldEntry := range cfg.StructMap.Fields {
		if fieldEntry.Ignore {
			continue
		}
		if !fieldEntry.OmitEmpty {
			total++
			continue
		}
		if !isEmptyValue(fieldEntry.ReflectRoute.TraverseToValue(target_rv)) {
			total++
			continue
		}
	}
	return total
}
