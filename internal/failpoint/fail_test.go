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

package failpoint

import (
	"reflect"
	"testing"
	"time"
)

func TestParseTerms(t *testing.T) {
	cases := []struct {
		terms    string
		hasError bool
	}{
		// off
		{"5", true},
		{"*off()", true},
		{"5*off()", true},
		{"5*off(nothing)", true},
		{"5*off(", true},
		{"5*off", false},

		// error
		{"10000error(oops)", true},
		{"10*error(oops)", false},
		{"1234*error(oops))", true},
		{"12342*error()", true},

		// panic
		{"1panic(oops)", true},
		{"1000000*panic(oops)", false},
		{"12345*panic(oops))", true},
		{"12*panic()", true},

		// delay
		{"1*delay(oops)", true},
		{"1000000*delay(-1)", true},
		{"1000000*delay(1)", false},

		// cascading terms
		{"1*delay(1)-", true},
		{"10*delay(2)->", true},
		{"11*delay(3)->10*off(", true},
		{"12*delay(4)->10*of", true},
		{"13*delay(5)->10*off->1000*panic(oops)", false},
	}

	for i, c := range cases {
		fp, err := NewFailpoint(t.Name(), c.terms)

		if (err != nil && !c.hasError) ||
			(err == nil && c.hasError) {

			t.Fatalf("[%v - %s] expected hasError=%v, but got %v", i, c.terms, c.hasError, err)
		}

		if err != nil {
			continue
		}

		if got := fp.Marshal(); !reflect.DeepEqual(got, c.terms) {
			t.Fatalf("[%v] expected %v, but got %v", i, c.terms, got)
		}
	}
}

func TestEvaluate(t *testing.T) {
	terms := "1*error(oops-)->1*off->1*delay(1000)->1*panic(panic)"

	fp, err := NewFailpoint(t.Name(), terms)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}

	injectedFn := func() error {
		if err := fp.Evaluate(); err != nil {
			return err
		}
		return nil
	}

	// should return oops- error
	if err := injectedFn(); err == nil || err.Error() != "oops-" {
		t.Fatalf("expected error %v, but got %v", "oops-", err)
	}

	// should return nil
	if err := injectedFn(); err != nil {
		t.Fatalf("expected nil, but got %v", err)
	}

	// should sleep 1s and return nil
	now := time.Now()
	err = injectedFn()
	du := time.Since(now)
	if err != nil {
		t.Fatalf("expected nil, but got %v", err)
	}
	if du < 1*time.Second {
		t.Fatalf("expected sleep 1s, but got %v", du)
	}

	// should panic
	defer func() {
		if err := recover(); err == nil || err.(string) != "panic" {
			t.Fatalf("should panic(panic), but got %v", err)
		}

		expected := "0*error(oops-)->0*off->0*delay(1000)->0*panic(panic)"
		if got := fp.Marshal(); got != expected {
			t.Fatalf("expected %v, but got %v", expected, got)
		}

		if err := injectedFn(); err != nil {
			t.Fatalf("expected nil, but got %v", err)
		}
	}()
	injectedFn()
}
