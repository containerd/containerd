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
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// defaultErrno is the errno runc and crun apply for SCMP_ACT_ERRNO and
// SCMP_ACT_TRACE when errnoRet is unset: EPERM.
const defaultErrno uint = 1

// errnoMask selects the part of errnoRet a runtime loads. The errno travels
// in the 16 data bits of the action: runc narrows errnoRet to an int16 and
// crun passes it through SCMP_ACT_ERRNO, which masks it, so 65537 is loaded
// as EPERM.
const errnoMask uint = 0xffff

// Restrictiveness levels, ordered from most restrictive (kill) to least
// (allow). Notify sits between Errno and Trace: it blocks the syscall pending
// a supervisor decision, making it more restrictive than Trace (which traps to
// a ptrace tracer) but less restrictive than Errno (which fails outright
// without supervisor intervention).
const (
	levelKillProcess = iota
	levelKillThread
	levelTrap
	levelErrno
	levelNotify
	levelTrace
	levelLog
	levelAllow
)

const levelUnknown = -1

// MoreRestrictive returns the more restrictive of two seccomp actions.
// An unknown action is ranked as the most restrictive there is and reported
// as SCMP_ACT_KILL_PROCESS, so that a caller writing the result into a
// profile writes an action a runtime loads rather than the unknown one back,
// and one no less restrictive than the rank it was given.
func MoreRestrictive(first, second specs.LinuxSeccompAction) specs.LinuxSeccompAction {
	return knownAction(moreRestrictive(first, second))
}

// LessRestrictive returns the less restrictive of two seccomp actions.
// An unknown action is ranked and reported as MoreRestrictive reports it.
func LessRestrictive(first, second specs.LinuxSeccompAction) specs.LinuxSeccompAction {
	return knownAction(lessRestrictive(first, second))
}

// knownAction replaces an action this package does not know with
// SCMP_ACT_KILL_PROCESS, the action of the rank the ranking gives it: an
// unknown action sorts above every known one, so standing in for it with
// anything less restrictive would report less than was asked for.
func knownAction(action specs.LinuxSeccompAction) specs.LinuxSeccompAction {
	if restrictiveness(action) == levelUnknown {
		return specs.ActKillProcess
	}

	return action
}

// moreRestrictive and lessRestrictive rank two actions without rewriting
// either. The merge picks between two clauses by asking which action wins
// and then keeping that clause whole (see pickClause), so it needs the
// action it passed in back, unknown or not: a profile the merge reads
// unvalidated, as IntersectSyscalls does, keeps the action it carries rather
// than having it turn into SCMP_ACT_KILL on one side of the comparison and
// not the other.
func moreRestrictive(first, second specs.LinuxSeccompAction) specs.LinuxSeccompAction {
	if restrictiveness(first) <= restrictiveness(second) {
		return first
	}

	return second
}

func lessRestrictive(first, second specs.LinuxSeccompAction) specs.LinuxSeccompAction {
	if restrictiveness(first) >= restrictiveness(second) {
		return first
	}

	return second
}

// actionsEquivalent reports whether two actions have the same effect.
// Unknown actions are equivalent only to themselves, so that unvalidated
// profiles (as Diff accepts) do not conflate distinct unknown actions.
func actionsEquivalent(first, second specs.LinuxSeccompAction) bool {
	level := restrictiveness(first)
	if level == levelUnknown {
		return first == second
	}

	return level == restrictiveness(second)
}

// canonicalAction spells SCMP_ACT_KILL_THREAD as SCMP_ACT_KILL, which
// libseccomp defines as the same action. Clauses are built in this form, so
// every result spells the action the same way.
func canonicalAction(action specs.LinuxSeccompAction) specs.LinuxSeccompAction {
	if action == specs.ActKillThread {
		return specs.ActKill
	}

	return action
}

func restrictiveness(action specs.LinuxSeccompAction) int {
	switch action {
	case specs.ActKillProcess:
		return levelKillProcess
	case specs.ActKill, specs.ActKillThread:
		return levelKillThread
	case specs.ActTrap:
		return levelTrap
	case specs.ActErrno:
		return levelErrno
	case specs.ActNotify:
		return levelNotify
	case specs.ActTrace:
		return levelTrace
	case specs.ActLog:
		return levelLog
	case specs.ActAllow:
		return levelAllow
	default:
		return levelUnknown
	}
}

// errnoSignificant reports whether ErrnoRet changes the runtime effect of an
// action. Runtimes only pass the errno value along for ERRNO and TRACE.
func errnoSignificant(action specs.LinuxSeccompAction) bool {
	return action == specs.ActErrno || action == specs.ActTrace
}

// runtimeErrno returns the errno a runtime applies for an action: the
// explicit value, narrowed to the bits a runtime loads (see errnoMask), or
// EPERM for ERRNO and TRACE, and nil for every other action, which ignores
// errnoRet. Clauses carry this form so that entries differing only in how
// they spell one errno compare equal.
func runtimeErrno(action specs.LinuxSeccompAction, ret *uint) *uint {
	if !errnoSignificant(action) {
		return nil
	}

	val := defaultErrno
	if ret != nil {
		val = *ret & errnoMask
	}

	return &val
}

// outputErrno returns the serialized form of an errno: nil when the runtime
// ignores it or would apply EPERM anyway, so that merge results spell the
// default the way most profiles do, and otherwise the value the runtime
// loads (see errnoMask).
func outputErrno(action specs.LinuxSeccompAction, ret *uint) *uint {
	loaded := runtimeErrno(action, ret)
	if loaded == nil || *loaded == defaultErrno {
		return nil
	}

	return loaded
}
