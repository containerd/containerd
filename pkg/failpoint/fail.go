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

// Package failpoint provides the code point in the path, which can be controlled
// by user's variable.
//
// Inspired by FreeBSD fail(9): https://freebsd.org/cgi/man.cgi?query=fail.
package failpoint

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EvalFn is the func type about delegated evaluation.
type EvalFn func() error

// Type is the type of failpoint to specifies which action to take.
type Type int

const (
	// TypeInvalid is invalid type
	TypeInvalid Type = iota
	// TypeOff takes no action
	TypeOff
	// TypeError triggers failpoint error with specified argument
	TypeError
	// TypePanic triggers panic with specified argument
	TypePanic
	// TypeDelay sleeps with the specified number of milliseconds
	TypeDelay
)

// String returns the name of type.
func (t Type) String() string {
	switch t {
	case TypeOff:
		return "off"
	case TypeError:
		return "error"
	case TypePanic:
		return "panic"
	case TypeDelay:
		return "delay"
	default:
		return "invalid"
	}
}

// Failpoint is used to add code points where error or panic may be injected by
// user. The user controlled variable will be parsed for how the error injected
// code should fire. There is the way to set the rule for failpoint.
//
//	<count>*<type>[(arg)][-><more terms>]
//
// The <type> argument specifies which action to take; it can be one of:
//
//	off:	Takes no action (does not trigger failpoint and no argument)
//	error:	Triggers failpoint error with specified argument(string)
//	panic:	Triggers panic with specified argument(string)
//	delay:	Sleep the specified number of milliseconds
//
// The <count>* modifiers prior to <type> control when <type> is executed. For
// example, "5*error(oops)" means "return error oops 5 times total". The
// operator -> can be used to express cascading terms. If you specify
// <term1>-><term2>, it means that if <term1> does not execute, <term2> will
// be evaluated. If you want the error injected code should fire in second
// call, you can specify "1*off->1*error(oops)".
//
// Inspired by FreeBSD fail(9): https://freebsd.org/cgi/man.cgi?query=fail.
type Failpoint struct {
	sync.Mutex

	fnName  string
	entries []*failpointEntry
}

// NewFailpoint returns failpoint control.
func NewFailpoint(fnName string, terms string) (*Failpoint, error) {
	entries, err := parseTerms([]byte(terms))
	if err != nil {
		return nil, err
	}

	return &Failpoint{
		fnName:  fnName,
		entries: entries,
	}, nil
}

// Evaluate evaluates a failpoint.
func (fp *Failpoint) Evaluate() error {
	fn := fp.DelegatedEval()
	return fn()
}

// DelegatedEval evaluates a failpoint but delegates to caller to fire that.
func (fp *Failpoint) DelegatedEval() EvalFn {
	var target *failpointEntry

	func() {
		fp.Lock()
		defer fp.Unlock()

		for _, entry := range fp.entries {
			if entry.count == 0 {
				continue
			}

			entry.count--
			target = entry
			break
		}
	}()

	if target == nil {
		return nopEvalFn
	}
	return target.evaluate
}

// Marshal returns the current state of control in string format.
func (fp *Failpoint) Marshal() string {
	fp.Lock()
	defer fp.Unlock()

	res := make([]string, 0, len(fp.entries))
	for _, entry := range fp.entries {
		res = append(res, entry.marshal())
	}
	return strings.Join(res, "->")
}

type failpointEntry struct {
	typ   Type
	arg   interface{}
	count int64
}

func newFailpointEntry() *failpointEntry {
	return &failpointEntry{
		typ:   TypeInvalid,
		count: 0,
	}
}

func (fpe *failpointEntry) marshal() string {
	base := fmt.Sprintf("%d*%s", fpe.count, fpe.typ)
	switch fpe.typ {
	case TypeOff:
		return base
	case TypeError, TypePanic:
		return fmt.Sprintf("%s(%s)", base, fpe.arg.(string))
	case TypeDelay:
		return fmt.Sprintf("%s(%d)", base, fpe.arg.(time.Duration)/time.Millisecond)
	default:
		return base
	}
}

func (fpe *failpointEntry) evaluate() error {
	switch fpe.typ {
	case TypeOff:
		return nil
	case TypeError:
		return fmt.Errorf("%v", fpe.arg)
	case TypePanic:
		panic(fpe.arg)
	case TypeDelay:
		time.Sleep(fpe.arg.(time.Duration))
		return nil
	default:
		panic("invalid failpoint type")
	}
}

func parseTerms(term []byte) ([]*failpointEntry, error) {
	var entry *failpointEntry
	var err error

	// count*type[(arg)]
	term, entry, err = parseTerm(term)
	if err != nil {
		return nil, err
	}

	res := []*failpointEntry{entry}

	// cascading terms
	for len(term) > 0 {
		if !bytes.HasPrefix(term, []byte("->")) {
			return nil, fmt.Errorf("invalid cascading terms: %s", string(term))
		}

		term = term[2:]
		term, entry, err = parseTerm(term)
		if err != nil {
			return nil, fmt.Errorf("failed to parse cascading term: %w", err)
		}

		res = append(res, entry)
	}
	return res, nil
}

func parseTerm(term []byte) ([]byte, *failpointEntry, error) {
	var err error
	var entry = newFailpointEntry()

	// count*
	term, err = parseInt64(term, '*', &entry.count)
	if err != nil {
		return nil, nil, err
	}

	// type[(arg)]
	term, err = parseType(term, entry)
	return term, entry, err
}

func parseType(term []byte, entry *failpointEntry) ([]byte, error) {
	var nameToTyp = map[string]Type{
		"off":    TypeOff,
		"error(": TypeError,
		"panic(": TypePanic,
		"delay(": TypeDelay,
	}

	var found bool
	for name, typ := range nameToTyp {
		if bytes.HasPrefix(term, []byte(name)) {
			found = true
			term = term[len(name):]
			entry.typ = typ
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("invalid type format: %s", string(term))
	}

	switch entry.typ {
	case TypePanic, TypeError:
		endIdx := bytes.IndexByte(term, ')')
		if endIdx <= 0 {
			return nil, fmt.Errorf("invalid argument for %s type", entry.typ)
		}
		entry.arg = string(term[:endIdx])
		return term[endIdx+1:], nil
	case TypeOff:
		// do nothing
		return term, nil
	case TypeDelay:
		var msVal int64
		var err error

		term, err = parseInt64(term, ')', &msVal)
		if err != nil {
			return nil, err
		}
		entry.arg = time.Millisecond * time.Duration(msVal)
		return term, nil
	default:
		panic("unreachable")
	}
}

func parseInt64(term []byte, terminate byte, val *int64) ([]byte, error) {
	i := 0

	for ; i < len(term); i++ {
		if b := term[i]; b < '0' || b > '9' {
			break
		}
	}

	if i == 0 || i == len(term) || term[i] != terminate {
		return nil, fmt.Errorf("failed to parse int64 because of invalid terminate byte: %s", string(term))
	}

	v, err := strconv.ParseInt(string(term[:i]), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse int64 from %s: %v", string(term[:i]), err)
	}

	*val = v
	return term[i+1:], nil
}

func nopEvalFn() error {
	return nil
}
