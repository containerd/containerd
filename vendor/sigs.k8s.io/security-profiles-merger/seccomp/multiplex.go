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
	"maps"
	"slices"

	specs "github.com/opencontainers/runtime-spec/specs-go"

	"sigs.k8s.io/security-profiles-merger/internal/merge"
)

// On the architectures in multiplexingArchitectures, a process reaches the
// socket and SysV IPC syscalls not only directly but also through one
// multiplexer, socketcall(2) or ipc(2), whose first argument names the
// syscall. libseccomp follows that: a rule for such a syscall is added as
// written for the direct syscall and a second time for the multiplexer,
// where the condition on the first argument is replaced by one matching the
// sub-call number and every other condition is kept on the same argument
// index, although the multiplexer passes the arguments of the call
// elsewhere. socket ALLOW if a0 == 2 therefore becomes socketcall ALLOW if
// a0 == 1, which allows every socket(2) made through socketcall.
//
// The rules of one syscall thus decide two paths. Under the sub-call test,
// libseccomp builds the multiplexed rules into a tree as it builds the rules
// of a syscall, so rules that test no first argument decide the multiplexer
// path of their sub-call as they decide the direct path. Rules that test it
// can end up with the same filter there, or with one that is a prefix of
// another's: with different results, libseccomp then refuses the rule with
// EEXIST or drops one of them, depending on the order they are added in,
// unless the multiplexer has an unconditional rule of its own added first,
// which then decides every call of the multiplexer and hides the
// multiplexed rules. Checked against libseccomp 2.6.1.
//
// The merge therefore reads the multiplexer path of each of these syscalls
// separately (see multiplexInput.view) and settles a result whose
// multiplexer path would be refused, or would permit more (intersection) or
// less (union) than an input's (see settleMultiplexed).

const (
	socketMultiplexer = "socketcall"
	ipcMultiplexer    = "ipc"
)

// multiplexedSyscalls maps every syscall libseccomp multiplexes to its
// multiplexer. Each is a different sub-call, so no two of them share a
// multiplexed rule. Checked against libseccomp 2.6.1 by adding a rule for
// every syscall name it knows on every architecture it knows.
//
//nolint:gochecknoglobals // immutable lookup table
var multiplexedSyscalls = map[string]string{
	"socket":      socketMultiplexer,
	"bind":        socketMultiplexer,
	"connect":     socketMultiplexer,
	"listen":      socketMultiplexer,
	"accept":      socketMultiplexer,
	"getsockname": socketMultiplexer,
	"getpeername": socketMultiplexer,
	"socketpair":  socketMultiplexer,
	"send":        socketMultiplexer,
	"recv":        socketMultiplexer,
	"sendto":      socketMultiplexer,
	"recvfrom":    socketMultiplexer,
	"shutdown":    socketMultiplexer,
	"setsockopt":  socketMultiplexer,
	"getsockopt":  socketMultiplexer,
	"sendmsg":     socketMultiplexer,
	"recvmsg":     socketMultiplexer,
	"accept4":     socketMultiplexer,
	"recvmmsg":    socketMultiplexer,
	"sendmmsg":    socketMultiplexer,
	"semop":       ipcMultiplexer,
	"semget":      ipcMultiplexer,
	"semctl":      ipcMultiplexer,
	"semtimedop":  ipcMultiplexer,
	"msgsnd":      ipcMultiplexer,
	"msgrcv":      ipcMultiplexer,
	"msgget":      ipcMultiplexer,
	"msgctl":      ipcMultiplexer,
	"shmat":       ipcMultiplexer,
	"shmdt":       ipcMultiplexer,
	"shmget":      ipcMultiplexer,
	"shmctl":      ipcMultiplexer,
}

// multiplexedBy returns the syscalls a multiplexer carries, sorted.
func multiplexedBy(multiplexer string) []string {
	var names []string

	for name, target := range multiplexedSyscalls {
		if target == multiplexer {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return names
}

// multiplexRules is what the multiplexer path needs to know about the rules
// one profile loads for a multiplexer or a multiplexed syscall.
type multiplexRules struct {
	// results folds the result of every rule, including conditional rules
	// an unconditional one hides from the direct syscall: on the
	// multiplexer, the unconditional rule becomes a conditional one on the
	// sub-call and hides nothing.
	results *clauseSummary
	// unconditional is the first unconditional rule, or nil.
	unconditional *clause
	// conditional reports whether a conditional rule was loaded.
	conditional bool
	// firstArg reports whether a rule tests the first argument, which the
	// multiplexed rule replaces by the sub-call.
	firstArg bool
	// wholeSubcall reports whether a rule tests no argument but the first,
	// so that its multiplexed rule matches every call of its sub-call.
	wholeSubcall bool
	// mixed reports whether the rules have different results.
	mixed bool
	// multiplexed holds the distinct multiplexed rules, without the
	// sub-call test, and seen their clauseKey.
	multiplexed []clause
	seen        map[string]struct{}
	// keys holds the action and filter of every rule, so that two profiles
	// loading the same rules can be recognized as such. The errno is left
	// out: a call gets the same action from the same rules whatever errno
	// they report, and the merge judges safety by action.
	keys map[string]struct{}
	// entry is the index of the first entry that loads a rule of the
	// syscall, where a report points.
	entry int
}

// add records one rule of the syscall.
func (r *multiplexRules) add(next clause) {
	if len(r.multiplexed) > 0 && !next.sameResult(r.multiplexed[0]) {
		r.mixed = true
	}

	r.results = r.results.fold(next)
	r.keys[actionKey(next)] = struct{}{}

	multiplexed := next
	multiplexed.args = slices.DeleteFunc(
		slices.Clone(next.args),
		func(arg specs.LinuxSeccompArg) bool { return arg.Index == 0 },
	)
	multiplexed.errnoRet = merge.ClonePtr(next.errnoRet)

	r.firstArg = r.firstArg || len(multiplexed.args) < len(next.args)
	r.wholeSubcall = r.wholeSubcall || multiplexed.unconditional()

	if key := clauseKey(multiplexed); !hasKey(r.seen, key) {
		r.seen[key] = struct{}{}
		r.multiplexed = append(r.multiplexed, multiplexed)
	}

	if next.unconditional() {
		if r.unconditional == nil {
			first := next
			first.errnoRet = merge.ClonePtr(next.errnoRet)
			r.unconditional = &first
		}

		return
	}

	r.conditional = true
}

func hasKey(set map[string]struct{}, key string) bool {
	_, ok := set[key]

	return ok
}

// refused reports whether libseccomp may refuse the multiplexed rules or
// drop one of them depending on the order they are added in: rules with
// different results where one of them matches the whole sub-call, or that
// do not form a safe shape without the sub-call test. Otherwise it builds
// them as it builds the rules of a syscall in that shape.
func (r *multiplexRules) refused() bool {
	return r.mixed && (r.wholeSubcall || !safeShape(r.multiplexed))
}

// collectMultiplexRules reads the rules a profile loads for the multiplexers
// and the syscalls they carry, skipping entries equal to the default as
// runtimes do.
func collectMultiplexRules(
	syscalls []specs.LinuxSyscall, def *clause,
) map[string]*multiplexRules {
	rules := make(map[string]*multiplexRules)

	relevant := func(name string) bool {
		_, multiplexed := multiplexedSyscalls[name]

		return multiplexed || name == socketMultiplexer || name == ipcMultiplexer
	}

	visit := func(idx int, name string, next clause) {
		current, ok := rules[name]
		if !ok {
			current = &multiplexRules{
				results:       nil,
				unconditional: nil,
				conditional:   false,
				firstArg:      false,
				wholeSubcall:  false,
				mixed:         false,
				multiplexed:   nil,
				seen:          make(map[string]struct{}),
				keys:          make(map[string]struct{}),
				entry:         idx,
			}
			rules[name] = current
		}

		current.add(next)
	}

	// Only the entries naming one of these syscalls are expanded, and only
	// for those names, so a merge pays for the rest of a profile once, in
	// collectRules. A name an entry repeats loads the same rules each time,
	// which add records once, so every name is read once per entry: the
	// expansion is then bounded by the syscalls there are to multiplex
	// rather than by how often an entry names one.
	for idx := range syscalls {
		entry := syscalls[idx]

		entry.Names = nil
		for _, name := range syscalls[idx].Names {
			if relevant(name) && !slices.Contains(entry.Names, name) {
				entry.Names = append(entry.Names, name)
			}
		}

		if len(entry.Names) > 0 {
			forEachClause([]specs.LinuxSyscall{entry}, def, nil,
				func(_ int, name string, next clause) { visit(idx, name, next) })
		}
	}

	return rules
}

// actionKey formats the action and the filter of a clause.
func actionKey(current clause) string {
	return string(current.action) + "|" + sortedArgsKey(current.args)
}

// multiplexInput is one input of a merge, read for the multiplexer path.
type multiplexInput struct {
	rules map[string]*multiplexRules
	def   *clause
}

func newMultiplexInput(syscalls []specs.LinuxSyscall, def *clause) multiplexInput {
	return multiplexInput{rules: collectMultiplexRules(syscalls, def), def: def}
}

// view returns the results a call of the multiplexer for the given
// syscall's sub-call can get, as their extremes. An unconditional rule of
// the multiplexer decides every such call, and with conditional ones, the
// call can get any result the multiplexer path has (see anyCall).
// Otherwise the call gets the result of a multiplexed rule of the syscall,
// or the default, which is left out when a rule of the syscall matches the
// whole sub-call, which it does when it tests no argument but the first,
// which the sub-call number replaces: libseccomp then either lets that rule
// decide the sub-call, drops the others, or refuses them. Whatever order
// libseccomp evaluates the rules in, the call gets one of these results, so
// this bounds it in both directions.
func (in multiplexInput) view(name string) *clauseSummary {
	multiplexer := multiplexedSyscalls[name]

	target := in.rules[multiplexer]
	if target != nil && target.unconditional != nil {
		return (*clauseSummary)(nil).fold(*target.unconditional)
	}

	if target != nil {
		return in.anyCall(multiplexer)
	}

	own := in.rules[name]

	var view *clauseSummary

	if own != nil {
		view = view.foldSummary(own.results)
	}

	if own == nil || !own.wholeSubcall {
		view = view.fold(*in.def)
	}

	return view
}

// anyCall returns the results any call of a multiplexer with conditional
// rules and no unconditional one can get: libseccomp builds those rules into
// one tree with the multiplexed rules, which it evaluates in its own order
// and does not always compile to a program matching them, so a call can end
// up with the result of any of these rules, whatever its sub-call, or with
// the default.
func (in multiplexInput) anyCall(multiplexer string) *clauseSummary {
	view := (*clauseSummary)(nil).foldSummary(in.rules[multiplexer].results)

	for _, name := range multiplexedBy(multiplexer) {
		if own := in.rules[name]; own != nil {
			view = view.foldSummary(own.results)
		}
	}

	return view.fold(*in.def)
}

// pickAnyCall moves a clause deciding calls of the multiplexer that no
// multiplexed rule of the result decides to the safe side of what those
// calls get in every input with conditional rules for it, where they can
// get the result of a multiplexed rule too (see anyCall). The direct path
// reads the multiplexer of such an input by its own rules only.
func (m ruleMerger) pickAnyCall(
	current clause, multiplexer string, inputs []multiplexInput,
) clause {
	for _, input := range inputs {
		if rules := input.rules[multiplexer]; rules != nil && rules.unconditional == nil {
			current = m.pickClause(current, input.anyCall(multiplexer).pick(m.intersect))
		}
	}

	return current
}

// sameDirect reports whether the multiplexer path of a syscall decides
// every call in both inputs as their direct path decides the syscall with
// any first argument: neither input has a rule for the multiplexer, and no
// rule of the syscall tests the first argument, so its multiplexed rules
// are its rules under the sub-call test. The direct path of a merge result
// is on the safe side of every input's, call by call, so this carries over
// to the multiplexer path, which extremes cannot show when the rules filter
// on more than the sub-call.
func (in multiplexInput) sameDirect(other multiplexInput, name string) bool {
	target := multiplexedSyscalls[name]
	if in.rules[target] != nil || other.rules[target] != nil {
		return false
	}

	own, others := in.rules[name], other.rules[name]

	return (own == nil || !own.firstArg) && (others == nil || !others.firstArg)
}

// sameRules reports whether the multiplexer path of a syscall applies the
// same actions in both inputs because they load the same rules for it and
// default to the same action, and neither has a rule for the multiplexer.
// The views of such inputs agree call by call, which their extremes cannot
// show when the rules filter on more than the sub-call.
func (in multiplexInput) sameRules(other multiplexInput, name string) bool {
	target := multiplexedSyscalls[name]
	if in.rules[target] != nil || other.rules[target] != nil ||
		!actionsEquivalent(in.def.action, other.def.action) {
		return false
	}

	own, others := in.rules[name], other.rules[name]
	if own == nil || others == nil {
		return own == nil && others == nil
	}

	return maps.Equal(own.keys, others.keys)
}

// within reports whether a result is on the safe side of bound for the
// merge direction: at least as restrictive for intersection, at least as
// permissive for union.
func (m ruleMerger) within(result, bound clause) bool {
	return actionsEquivalent(m.pick(result.action, bound.action), result.action)
}

// covers reports whether the multiplexer path of a syscall in the result is
// on the safe side of the same path in an input: call by call where both
// read it as their direct path or load the same rules, and otherwise by the
// result's view in its least safe extreme against the input's view in its
// safest.
func (m ruleMerger) covers(result, input multiplexInput, name string) bool {
	if result.sameRules(input, name) || result.sameDirect(input, name) {
		return true
	}

	return m.within(result.view(name).pick(!m.intersect), input.view(name).pick(m.intersect))
}

// settleMultiplexed makes a merge result safe on the multiplexer path, for
// a result that covers a multiplexing architecture. The result is safe on
// the direct path already; syscalls holds its entries, one name each, def is
// its default, and inputs are what it was merged from.
//
// For each multiplexer, the result must not load rules libseccomp refuses
// there, and the multiplexer path of every syscall it carries must be on
// the safe side of that path in every input. Conditional rules of the
// multiplexer itself interleave with the multiplexed rules in libseccomp's
// own order, and an unconditional one decides every sub-call, so the
// multiplexer collapses to one unconditional rule when it has conditional
// rules or its unconditional rule is on the wrong side of an input: the
// clause collapse picks for its rules, moved to the safe side of every
// input's view of every sub-call. An input with conditional multiplexer
// rules can give any call of the multiplexer the result of a multiplexed
// rule (see anyCall), so the rule, or the default of a result without one,
// must be on the safe side of that as well, and a result gets such a rule
// where its default is not. Where the rule is the default, it is dropped
// and the multiplexed rules decide again. A multiplexed syscall then
// collapses the same way when libseccomp may refuse its multiplexed rules
// (see multiplexRules.refused), or, without a multiplexer rule hiding them,
// when its multiplexer path is on the wrong side of an input's; the
// collapsed rule decides its whole sub-call, so the view becomes that
// rule. Every rule a collapse picks is on the safe side of what it
// replaces, so the direct path stays safe. The second result reports
// whether anything had to be settled, rather than only dropped where that
// is more precise (see settleMultiplexer).
func (m ruleMerger) settleMultiplexed(
	syscalls []specs.LinuxSyscall, def *clause, inputs []multiplexInput,
) ([]specs.LinuxSyscall, bool) {
	result := newMultiplexInput(syscalls, def)
	replaced := make(map[string]*clause)
	unhidden := make(map[string]bool)

	for _, target := range []string{ipcMultiplexer, socketMultiplexer} {
		names := multiplexedBy(target)

		hiding := m.settleMultiplexer(result, target, names, inputs, replaced, unhidden)

		for _, name := range names {
			m.settleMultiplexedSyscall(result, name, hiding, inputs, replaced)
		}
	}

	if len(replaced) == 0 {
		return syscalls, false
	}

	settled := slices.DeleteFunc(slices.Clone(syscalls), func(entry specs.LinuxSyscall) bool {
		_, ok := replaced[entry.Names[0]]

		return ok
	})

	for _, name := range slices.Sorted(maps.Keys(replaced)) {
		settled = appendRules(settled, name, def, replaced[name], nil)
	}

	return settled, len(replaced) > len(unhidden)
}

// settleMultiplexer settles the multiplexer's own rules and reports whether
// the result keeps an unconditional multiplexer rule, which hides every
// multiplexed rule. Such a rule comes from the direct merge of the
// multiplexer, where an errno the inputs' defaults differ in can decide
// whether the result spells it out at all, and it can hide what the
// multiplexed rules would permit (intersection) or deny (union) as the
// inputs do. A rule that is safe to keep but differs from the default in
// its errno only is therefore dropped where that is safe too (see unhides),
// which unhidden records, since it settles nothing.
func (m ruleMerger) settleMultiplexer(
	result multiplexInput, target string, names []string,
	inputs []multiplexInput, replaced map[string]*clause, unhidden map[string]bool,
) bool {
	current := result.rules[target]
	if current == nil {
		// The default decides every call of the multiplexer that no
		// multiplexed rule of the result decides.
		collapsed := m.pickAnyCall(*result.def, target, inputs)
		if collapsed.sameResult(*result.def) {
			return false
		}

		for _, name := range names {
			collapsed = m.pickInputs(collapsed, name, inputs)
		}

		result.replace(target, collapsed, replaced)

		return replaced[target] != nil
	}

	if !current.conditional && m.hidesSafely(*current.unconditional, names, inputs) &&
		m.pickAnyCall(*current.unconditional, target, inputs).sameResult(*current.unconditional) {
		if m.unhides(result, target, names, inputs) {
			replaced[target] = nil
			unhidden[target] = true

			return false
		}

		return true
	}

	collapsed := current.results.pick(m.intersect)
	if current.unconditional == nil {
		collapsed = m.pickClause(collapsed, *result.def)
	}

	collapsed = m.pickAnyCall(collapsed, target, inputs)

	for _, name := range names {
		collapsed = m.pickInputs(collapsed, name, inputs)
	}

	result.replace(target, collapsed, replaced)

	return replaced[target] != nil
}

// unhides reports whether the result's unconditional multiplexer rule is
// better dropped, and drops it from result if so. The rule must apply the
// default's action, which then decides every call of the multiplexer that
// no multiplexed rule decides. The multiplexed rules decide the sub-calls
// then: they must not be refused, must be on the safe side of every input's
// multiplexer path, nowhere more restrictive (intersection) or more
// permissive (union) than the rule, and somewhere different from it.
func (m ruleMerger) unhides(
	result multiplexInput, target string, names []string, inputs []multiplexInput,
) bool {
	rule := *result.rules[target].unconditional
	if !actionsEquivalent(rule.action, result.def.action) {
		return false
	}

	saved := result.rules[target]
	delete(result.rules, target)

	gains := false

	for _, name := range names {
		own := result.rules[name]
		view := result.view(name)

		if own != nil && own.refused() || !m.within(rule, view.pick(m.intersect)) ||
			slices.ContainsFunc(inputs, func(input multiplexInput) bool {
				return !m.covers(result, input, name)
			}) {
			result.rules[target] = saved

			return false
		}

		gains = gains || !actionsEquivalent(view.pick(!m.intersect).action, rule.action)
	}

	if !gains {
		result.rules[target] = saved
	}

	return gains
}

// replace records the unconditional rule that replaces the rules of a
// syscall, or none where it is the default, which decides the calls then,
// and reads the result that way from here on.
func (in multiplexInput) replace(name string, rule clause, replaced map[string]*clause) {
	if rule.sameResult(*in.def) {
		replaced[name] = nil
		delete(in.rules, name)

		return
	}

	entry := 0
	if current := in.rules[name]; current != nil {
		entry = current.entry
	}

	rule.args = nil
	rule.errnoRet = merge.ClonePtr(rule.errnoRet)
	replaced[name] = &rule
	in.rules[name] = &multiplexRules{
		results:       (*clauseSummary)(nil).fold(rule),
		unconditional: &rule,
		conditional:   false,
		firstArg:      false,
		wholeSubcall:  true,
		mixed:         false,
		multiplexed:   []clause{rule},
		seen:          map[string]struct{}{clauseKey(rule): {}},
		keys:          map[string]struct{}{actionKey(rule): {}},
		entry:         entry,
	}
}

// hidesSafely reports whether an unconditional multiplexer rule is on the
// safe side of every input's view of every sub-call it decides.
func (m ruleMerger) hidesSafely(rule clause, names []string, inputs []multiplexInput) bool {
	for _, name := range names {
		for _, input := range inputs {
			if !m.within(rule, input.view(name).pick(m.intersect)) {
				return false
			}
		}
	}

	return true
}

// pickInputs moves a clause to the safe side of every input's view of the
// syscall's sub-call.
func (m ruleMerger) pickInputs(current clause, name string, inputs []multiplexInput) clause {
	for _, input := range inputs {
		current = m.pickClause(current, input.view(name).pick(m.intersect))
	}

	return current
}

// settleMultiplexedSyscall settles the rules of one multiplexed syscall.
// hiding reports whether an unconditional multiplexer rule hides its
// multiplexed rules.
func (m ruleMerger) settleMultiplexedSyscall(
	result multiplexInput, name string, hiding bool,
	inputs []multiplexInput, replaced map[string]*clause,
) {
	own := result.rules[name]
	refused := own != nil && own.refused()

	if !refused && (hiding || !slices.ContainsFunc(inputs, func(input multiplexInput) bool {
		return !m.covers(result, input, name)
	})) {
		return
	}

	collapsed := *result.def
	if own != nil {
		collapsed = own.results.pick(m.intersect)
		if own.unconditional == nil {
			collapsed = m.pickClause(collapsed, *result.def)
		}
	}

	if !hiding {
		collapsed = m.pickInputs(collapsed, name, inputs)
	}

	result.replace(name, collapsed, replaced)
}
