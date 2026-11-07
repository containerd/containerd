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
	"slices"

	specs "github.com/opencontainers/runtime-spec/specs-go"

	"sigs.k8s.io/security-profiles-merger/internal/merge"
)

// flagTsync is SECCOMP_FILTER_FLAG_TSYNC, which the runtime-spec lists but
// specs-go has no constant for. It changes nothing a filter permits: runc
// accepts and ignores it, since it synchronizes the filter across threads
// anyway, and crun passes it on to seccomp(2).
const flagTsync specs.LinuxSeccompFlag = "SECCOMP_FILTER_FLAG_TSYNC"

// flagPolarity classifies a seccomp filter flag by what setting it does to
// the confined process, which decides how a merge combines it.
type flagPolarity int

const (
	// flagPermissive loosens confinement: SECCOMP_FILTER_FLAG_SPEC_ALLOW
	// disables the Speculative Store Bypass mitigation. Intersection keeps
	// such a flag only if every input sets it; union keeps it if any does.
	flagPermissive flagPolarity = iota
	// flagHardening tightens confinement or auditing:
	// SECCOMP_FILTER_FLAG_LOG logs every action other than allow.
	// Intersection keeps such a flag if any input sets it; union only if
	// every input does. SECCOMP_FILTER_FLAG_TSYNC loosens nothing either and
	// is merged the same way. Unknown flags are treated as hardening, which
	// is the conservative choice for intersection.
	flagHardening
	// flagListener changes how notifications are delivered:
	// SECCOMP_FILTER_FLAG_WAIT_KILLABLE_RECV only matters together with a
	// listener, which is taken from the first profile that sets one, so the
	// flag is taken from that profile too.
	flagListener
)

func polarityOf(flag specs.LinuxSeccompFlag) flagPolarity {
	switch flag {
	case specs.LinuxSeccompFlagSpecAllow:
		return flagPermissive
	case specs.LinuxSeccompFlagWaitKillableRecv:
		return flagListener
	case specs.LinuxSeccompFlagLog, flagTsync:
		return flagHardening
	default:
		return flagHardening
	}
}

// keepFlag decides whether a flag survives a merge given where it is set.
// A permissive flag under intersection and a hardening flag under union
// need every profile; the other two combinations need any profile. A
// listener flag belongs to the listener, so it follows the profile the
// listener comes from and is dropped when that profile does not set it.
func keepFlag(polarity flagPolarity, inLeft, inRight, intersect, listenerFromLeft bool) bool {
	if polarity == flagListener {
		if listenerFromLeft {
			return inLeft
		}

		return inRight
	}

	needsEvery := (polarity == flagPermissive) == intersect
	if needsEvery {
		return inLeft && inRight
	}

	return inLeft || inRight
}

// mergeFlags combines the flag lists of two profiles according to each
// flag's polarity.
//
// A nil list is not an empty one: it leaves the flags to the runtime, and
// runc since 1.2 and crun then set SECCOMP_FILTER_FLAG_SPEC_ALLOW, which a
// list that is set but empty turns off. A nil list is therefore merged as
// one naming that flag, and the result is nil again where the flag survives
// through an input that left it to the runtime and nothing else does. A
// list cannot leave one flag to the runtime and set another, so where the
// result holds other flags it names the flag instead.
func mergeFlags(
	left, right []specs.LinuxSeccompFlag, intersect, listenerFromLeft bool,
) []specs.LinuxSeccompFlag {
	result := []specs.LinuxSeccompFlag{}

	for _, flag := range merge.UnionSlice(left, right) {
		inLeft := slices.Contains(left, flag)
		inRight := slices.Contains(right, flag)

		if keepFlag(polarityOf(flag), inLeft, inRight, intersect, listenerFromLeft) {
			result = append(result, flag)
		}
	}

	if !slices.Contains(result, specs.LinuxSeccompFlagSpecAllow) &&
		specAllowLeftToRuntime(left, right, intersect) {
		if len(result) == 0 {
			return nil
		}

		result = append(result, specs.LinuxSeccompFlagSpecAllow)
	}

	slices.Sort(result)

	return result
}

// specAllowLeftToRuntime reports whether SECCOMP_FILTER_FLAG_SPEC_ALLOW
// survives a merge that keeps no input's spelling of it, through an input
// that leaves it to the runtime as a nil list does. In an intersection it
// does unless an input turns the flag off, in a union when any input leaves
// it to the runtime.
func specAllowLeftToRuntime(left, right []specs.LinuxSeccompFlag, intersect bool) bool {
	if !intersect {
		return left == nil || right == nil
	}

	off := func(flags []specs.LinuxSeccompFlag) bool {
		return flags != nil && !slices.Contains(flags, specs.LinuxSeccompFlagSpecAllow)
	}

	return !off(left) && !off(right)
}

// flagsForListener drops SECCOMP_FILTER_FLAG_TSYNC from the flags of a
// result that has a listener. The kernel refuses the flag next to the one
// that creates the listener, and crun passes both, so such a result would
// not load there however loadable the inputs were, of which one may set the
// flag and another the listener. Dropping it loosens nothing (see
// flagTsync), and a list it was alone in stays set but empty.
func flagsForListener(
	flags []specs.LinuxSeccompFlag, listenerPath string,
) []specs.LinuxSeccompFlag {
	if listenerPath == "" {
		return flags
	}

	return slices.DeleteFunc(flags, func(flag specs.LinuxSeccompFlag) bool {
		return flag == flagTsync
	})
}

// normalizeFlags returns the flags of a single profile without duplicates,
// keeping a list that is set but empty apart from a nil one (see
// mergeFlags).
func normalizeFlags(flags []specs.LinuxSeccompFlag) []specs.LinuxSeccompFlag {
	if flags == nil {
		return nil
	}

	return append([]specs.LinuxSeccompFlag{}, merge.DeduplicateSlice(flags)...)
}

// loadedFlags returns the flags a runtime sets for a list: the list itself,
// or SECCOMP_FILTER_FLAG_SPEC_ALLOW for a nil one, which is what runc since
// 1.2 and crun make of it.
func loadedFlags(flags []specs.LinuxSeccompFlag) []specs.LinuxSeccompFlag {
	if flags == nil {
		return []specs.LinuxSeccompFlag{specs.LinuxSeccompFlagSpecAllow}
	}

	return flags
}
