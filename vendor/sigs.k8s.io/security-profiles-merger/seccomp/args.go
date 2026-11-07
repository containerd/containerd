/*
Copyright The Kubernetes Authors.

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

package seccomp

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// canonicalArg returns the condition as a runtime evaluates it: libseccomp
// reads valueTwo only for SCMP_CMP_MASKED_EQ, so it is cleared for every
// other operator, and for SCMP_CMP_MASKED_EQ it masks valueTwo with the mask
// in value before comparing, so bits outside the mask are cleared. Without
// this, conditions that differ only in ignored bits would be treated as
// different filters.
func canonicalArg(arg specs.LinuxSeccompArg) specs.LinuxSeccompArg {
	if arg.Op == specs.OpMaskedEqual {
		arg.ValueTwo &= arg.Value
	} else {
		arg.ValueTwo = 0
	}

	return arg
}

// tautology reports whether a canonical condition holds for every value.
// libseccomp drops such a condition from its rule: a SCMP_CMP_MASKED_EQ
// with an empty mask.
func tautology(arg specs.LinuxSeccompArg) bool {
	return arg.Op == specs.OpMaskedEqual && arg.Value == 0
}

// sortedArgs returns a sorted, canonical copy of the argument filters.
func sortedArgs(args []specs.LinuxSeccompArg) []specs.LinuxSeccompArg {
	if len(args) == 0 {
		return nil
	}

	cloned := make([]specs.LinuxSeccompArg, len(args))
	for idx, arg := range args {
		cloned[idx] = canonicalArg(arg)
	}

	sortArgs(cloned)

	return cloned
}

func sortArgs(args []specs.LinuxSeccompArg) {
	slices.SortFunc(args, compareArg)
}

// compareArg orders two conditions by index, value, valueTwo and operator.
func compareArg(left, right specs.LinuxSeccompArg) int {
	return cmp.Or(
		cmp.Compare(left.Index, right.Index),
		cmp.Compare(left.Value, right.Value),
		cmp.Compare(left.ValueTwo, right.ValueTwo),
		cmp.Compare(left.Op, right.Op),
	)
}

// argsKey returns a canonical string for a set of argument filters so that
// filters differing only in order or in an ignored valueTwo compare equal.
// Empty args yield "".
func argsKey(args []specs.LinuxSeccompArg) string {
	return sortedArgsKey(sortedArgs(args))
}

// argKeyBytes sizes the key builder for a typical condition, which runs to
// about eighteen bytes ("0:SCMP_CMP_EQ:0:0;"). It is a hint, not a bound: a
// longer one costs a regrowth, which is what this saves on the hot path.
//
// Sizing it generously would not be free. Builder.String hands out a string
// backed by the whole buffer without copying it, so every byte reserved
// here stays live for as long as the key does, and these keys sit in maps
// for the length of a merge. Measured over the artifact-sized benchmarks, a
// hint of 24 allocates fewer bytes than no hint at all while removing the
// same 30% of allocations; a hint of 64 removes those allocations but ends
// up costing more bytes than not growing.
const argKeyBytes = 24

// sortedArgsKey formats args that are already sorted and canonical, as
// clause args always are, without copying them first. It is the hot path of
// the merge: every clause comparison and grouping goes through it.
func sortedArgsKey(args []specs.LinuxSeccompArg) string {
	if len(args) == 0 {
		return ""
	}

	var builder strings.Builder

	builder.Grow(len(args) * argKeyBytes)

	for _, arg := range args {
		builder.WriteString(strconv.FormatUint(uint64(arg.Index), 10))
		builder.WriteByte(':')
		builder.WriteString(string(arg.Op))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatUint(arg.Value, 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatUint(arg.ValueTwo, 10))
		builder.WriteByte(';')
	}

	return builder.String()
}

func groupArgsByIndex(
	args []specs.LinuxSeccompArg,
) map[uint][]specs.LinuxSeccompArg {
	grouped := make(map[uint][]specs.LinuxSeccompArg)

	for _, arg := range args {
		grouped[arg.Index] = append(grouped[arg.Index], arg)
	}

	for idx := range grouped {
		sortArgs(grouped[idx])
	}

	return grouped
}

// conjoinArgs returns a single filter matching exactly the calls matched by
// both inputs. OCI argument filters are AND-joined and a runtime accepts at
// most one condition per argument index, so the conjunction exists only when
// every index present on both sides carries one condition that says what
// both say: the same condition, or one of two where the one implies the
// other (see condImplies), which is then the conjunction of the two. The
// second return value is false when no such filter exists.
func conjoinArgs(
	left, right []specs.LinuxSeccompArg,
) ([]specs.LinuxSeccompArg, bool) {
	leftByIndex := groupArgsByIndex(left)
	rightByIndex := groupArgsByIndex(right)

	result := make([]specs.LinuxSeccompArg, 0, len(left)+len(right))

	for idx, leftGroup := range leftByIndex {
		rightGroup, shared := rightByIndex[idx]
		if !shared {
			result = append(result, leftGroup...)

			continue
		}

		stronger, ok := strongerGroup(leftGroup, rightGroup)
		if !ok {
			return nil, false
		}

		result = append(result, stronger...)
	}

	for idx, rightGroup := range rightByIndex {
		if _, ok := leftByIndex[idx]; !ok {
			result = append(result, rightGroup...)
		}
	}

	sortArgs(result)

	return result, true
}

// strongerGroup returns the conditions of one argument index that match
// exactly the calls both groups match: either group when they are equal, or
// the single condition that implies the single condition of the other. A
// group holds several conditions only in unvalidated input, where nothing
// but equality is read from it.
func strongerGroup(left, right []specs.LinuxSeccompArg) ([]specs.LinuxSeccompArg, bool) {
	switch {
	case slices.Equal(left, right):
		return left, true
	case len(left) != 1 || len(right) != 1:
		return nil, false
	case condImplies(left[0], right[0]):
		return left, true
	case condImplies(right[0], left[0]):
		return right, true
	default:
		return nil, false
	}
}

// argsCover reports whether the filter wide matches every call the filter
// narrow matches: each condition of wide is implied by a condition narrow
// puts on the same argument. A filter whose conditions all appear in the
// other covers it, as does one whose conditions say less: "a0 != 40" covers
// "a0 == 2". It is conservative: false means wide may still cover narrow.
func argsCover(wide, narrow []specs.LinuxSeccompArg) bool {
	for _, loose := range wide {
		if !slices.ContainsFunc(narrow, func(strict specs.LinuxSeccompArg) bool {
			return strict.Index == loose.Index && condImplies(strict, loose)
		}) {
			return false
		}
	}

	return true
}

// condImplies reports whether every value the condition strong matches is
// one the condition weak matches, both read on one argument. It is
// conservative: false means the implication may still hold.
//
// Two different conditions are compared only when neither holds a value
// above 32 bits. libseccomp compares the lower 32 bits alone on a 32-bit
// architecture, where "a0 == 0x100000028" matches 40 and so does not imply
// "a0 != 40", and below that width a condition reads the same on every
// architecture.
func condImplies(strong, weak specs.LinuxSeccompArg) bool {
	if strong == weak {
		return true
	}

	if wideArg(strong) || wideArg(weak) {
		return false
	}

	switch strong.Op {
	case specs.OpEqualTo:
		return condHolds(weak, strong.Value)
	case specs.OpMaskedEqual:
		return maskImplies(strong, weak)
	case specs.OpNotEqual, specs.OpLessThan, specs.OpLessEqual,
		specs.OpGreaterEqual, specs.OpGreaterThan:
		return rangeImplies(strong, weak)
	default:
		return false
	}
}

// rangeImplies is condImplies for a condition that matches a range of
// values, if any: it implies a range holding its own, and that the argument
// is not a value outside it.
func rangeImplies(strong, weak specs.LinuxSeccompArg) bool {
	low, high, ok := condInterval(strong)
	if !ok {
		return false
	}

	if weak.Op == specs.OpNotEqual {
		return weak.Value < low || weak.Value > high
	}

	weakLow, weakHigh, weakOk := condInterval(weak)

	return weakOk && weakLow <= low && high <= weakHigh
}

// maskImplies is condImplies for a masked comparison: it fixes the bits of
// its mask, so it implies a masked comparison of a part of those bits with
// the same values, and that the argument is not a value that differs from
// its own in one of them.
func maskImplies(strong, weak specs.LinuxSeccompArg) bool {
	switch weak.Op {
	case specs.OpMaskedEqual:
		return weak.Value&^strong.Value == 0 && strong.ValueTwo&weak.Value == weak.ValueTwo
	case specs.OpNotEqual:
		return weak.Value&strong.Value != strong.ValueTwo
	case specs.OpEqualTo, specs.OpLessThan, specs.OpLessEqual,
		specs.OpGreaterEqual, specs.OpGreaterThan:
		return false
	default:
		return false
	}
}

// argsDisjoint reports whether two filters provably never match the same
// call. It is conservative: false means the filters may overlap.
func argsDisjoint(left, right []specs.LinuxSeccompArg) bool {
	for _, leftArg := range left {
		for _, rightArg := range right {
			if leftArg.Index == rightArg.Index && condsDisjoint(leftArg, rightArg) {
				return true
			}
		}
	}

	return false
}

// condsDisjoint reports whether two conditions on the same argument can never
// both hold.
func condsDisjoint(left, right specs.LinuxSeccompArg) bool {
	if left.Op == specs.OpEqualTo {
		return !condHolds(right, left.Value)
	}

	if right.Op == specs.OpEqualTo {
		return !condHolds(left, right.Value)
	}

	leftLo, leftHi, leftOk := condInterval(left)
	rightLo, rightHi, rightOk := condInterval(right)

	if !leftOk || !rightOk {
		return false
	}

	return leftHi < rightLo || rightHi < leftLo
}

// condInterval returns the inclusive value range (low, high) matched by a
// comparison operator. The third result is false for operators that do not
// describe a contiguous range, or for ranges that are empty.
func condInterval(arg specs.LinuxSeccompArg) (uint64, uint64, bool) {
	switch arg.Op {
	case specs.OpLessThan:
		if arg.Value == 0 {
			return 0, 0, false
		}

		return 0, arg.Value - 1, true
	case specs.OpLessEqual:
		return 0, arg.Value, true
	case specs.OpGreaterThan:
		if arg.Value == math.MaxUint64 {
			return 0, 0, false
		}

		return arg.Value + 1, math.MaxUint64, true
	case specs.OpGreaterEqual:
		return arg.Value, math.MaxUint64, true
	case specs.OpEqualTo:
		return arg.Value, arg.Value, true
	case specs.OpNotEqual, specs.OpMaskedEqual:
		return 0, 0, false
	default:
		return 0, 0, false
	}
}

// condHolds evaluates a single argument condition against a concrete value
// the way the program libseccomp compiles for a 64-bit architecture does.
// Unknown operators never hold.
func condHolds(arg specs.LinuxSeccompArg, value uint64) bool {
	switch arg.Op {
	case specs.OpNotEqual:
		return value != arg.Value
	case specs.OpLessThan:
		return value < arg.Value
	case specs.OpLessEqual:
		return value <= arg.Value
	case specs.OpEqualTo:
		return value == arg.Value
	case specs.OpGreaterEqual:
		return value >= arg.Value
	case specs.OpGreaterThan:
		return value > arg.Value
	case specs.OpMaskedEqual:
		// libseccomp masks the datum as well, see canonicalArg.
		return value&arg.Value == arg.ValueTwo&arg.Value
	default:
		return false
	}
}
