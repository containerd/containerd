package typeurl

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
)

type test struct {
	Name string
	Age  int
}

func clear() {
	registry = make(map[reflect.Type]string)
}

func TestRegisterPointerGetPointer(t *testing.T) {
	clear()
	expected := filepath.Join(Prefix, "test")
	Register(&test{}, "test")

	url, err := TypeURL(&test{})
	if err != nil {
		t.Fatal(err)
	}
	if url != expected {
		t.Fatalf("expected %q but received %q", expected, url)
	}
}

func TestMarshal(t *testing.T) {
	clear()
	expected := filepath.Join(Prefix, "test")
	Register(&test{}, "test")

	v := &test{
		Name: "koye",
		Age:  6,
	}
	any, err := MarshalAny(v)
	if err != nil {
		t.Fatal(err)
	}
	if any.TypeUrl != expected {
		t.Fatalf("expected %q but received %q", expected, any.TypeUrl)
	}
}

func TestMarshalUnmarshal(t *testing.T) {
	clear()
	Register(&test{}, "test")

	v := &test{
		Name: "koye",
		Age:  6,
	}
	any, err := MarshalAny(v)
	if err != nil {
		t.Fatal(err)
	}
	nv, err := UnmarshalAny(any)
	if err != nil {
		t.Fatal(err)
	}
	td, ok := nv.(*test)
	if !ok {
		t.Fatal("expected value to cast to *test")
	}
	if td.Name != "koye" {
		t.Fatal("invalid name")
	}
	if td.Age != 6 {
		t.Fatal("invalid age")
	}
}

func TestIs(t *testing.T) {
	clear()
	Register(&test{}, "test")

	v := &test{
		Name: "koye",
		Age:  6,
	}
	any, err := MarshalAny(v)
	if err != nil {
		t.Fatal(err)
	}
	if !Is(any, &test{}) {
		t.Fatal("Is(any, test{}) should be true")
	}
}

func TestMarshalEvent(t *testing.T) {
	for _, testcase := range []struct {
		event interface{}
		url   string
	}{
		{
			event: &eventsapi.TaskStart{},
			url:   "types.containerd.io/containerd.services.events.v1.TaskStart",
		},

		{
			event: &eventsapi.NamespaceUpdate{},
			url:   "types.containerd.io/containerd.services.events.v1.NamespaceUpdate",
		},
	} {
		t.Run(fmt.Sprintf("%T", testcase.event), func(t *testing.T) {
			a, err := MarshalAny(testcase.event)
			if err != nil {
				t.Fatal(err)
			}
			if a.TypeUrl != testcase.url {
				t.Fatalf("unexpected url: %v != %v", a.TypeUrl, testcase.url)
			}

			v, err := UnmarshalAny(a)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(v, testcase.event) {
				t.Fatalf("round trip failed %v != %v", v, testcase.event)
			}
		})
	}
}

func BenchmarkMarshalEvent(b *testing.B) {
	ev := &eventsapi.TaskStart{}
	expected, err := MarshalAny(ev)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		a, err := MarshalAny(ev)
		if err != nil {
			b.Fatal(err)
		}
		if a.TypeUrl != expected.TypeUrl {
			b.Fatalf("incorrect type url: %v != %v", a, expected)
		}
	}
}
