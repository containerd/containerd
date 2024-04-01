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

package gc

import (
	"context"
	"reflect"
	"testing"
)

func TestTricolorBasic(t *testing.T) {
	roots := []string{"A", "C"}
	all := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	refs := map[string][]string{
		"A": {"B"},
		"B": {"A"},
		"C": {"D", "F", "B"},
		"E": {"F", "G"},
		"F": {"H"},
	}

	expected := toNodes([]string{"A", "B", "C", "D", "F", "H"})

	reachable, err := Tricolor(toNodes(roots), lookup(refs))
	if err != nil {
		t.Fatal(err)
	}

	var sweeped []Node
	for _, a := range toNodes(all) {
		if _, ok := reachable[a]; ok {
			sweeped = append(sweeped, a)
		}
	}

	if !reflect.DeepEqual(sweeped, expected) {
		t.Fatalf("incorrect unreachable set: %v != %v", sweeped, expected)
	}
}

func TestConcurrentBasic(t *testing.T) {
	roots := []string{"A", "C"}
	all := []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"}
	refs := map[string][]string{
		"A": {"B"},
		"B": {"A"},
		"C": {"D", "F", "B"},
		"E": {"F", "G"},
		"F": {"H"},
		"G": {"I"},
	}

	expected := toNodes([]string{"A", "B", "C", "D", "F", "H"})

	ctx := context.Background()
	rootC := make(chan Node)
	go func() {
		writeNodes(ctx, rootC, toNodes(roots))
		close(rootC)
	}()

	reachable, err := ConcurrentMark(ctx, rootC, lookupc(refs))
	if err != nil {
		t.Fatal(err)
	}

	var sweeped []Node
	for _, a := range toNodes(all) {
		if _, ok := reachable[a]; ok {
			sweeped = append(sweeped, a)
		}
	}

	if !reflect.DeepEqual(sweeped, expected) {
		t.Fatalf("incorrect unreachable set: %v != %v", sweeped, expected)
	}
}

func writeNodes(ctx context.Context, nc chan<- Node, nodes []Node) {
	for _, n := range nodes {
		select {
		case nc <- n:
		case <-ctx.Done():
			return
		}
	}
}

func lookup(refs map[string][]string) func(id Node) ([]Node, error) {
	return func(ref Node) ([]Node, error) {
		return toNodes(refs[ref.Key]), nil
	}
}

func lookupc(refs map[string][]string) func(context.Context, Node, func(Node)) error {
	return func(ctx context.Context, ref Node, fn func(Node)) error {
		for _, n := range toNodes(refs[ref.Key]) {
			fn(n)
		}
		return nil
	}
}

func toNodes(s []string) []Node {
	n := make([]Node, len(s))
	for i := range s {
		n[i] = Node{
			Key: s[i],
		}
	}
	return n
}
