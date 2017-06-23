package events

import (
	"fmt"
	"reflect"
	"testing"

	events "github.com/containerd/containerd/api/services/events/v1"
)

func TestMarshalEvent(t *testing.T) {
	for _, testcase := range []struct {
		event Event
		url   string
	}{
		{
			event: &events.TaskStart{},
			url:   "types.containerd.io/containerd.services.events.v1.TaskStart",
		},

		{
			event: &events.NamespaceUpdate{},
			url:   "types.containerd.io/containerd.services.events.v1.NamespaceUpdate",
		},
	} {
		t.Run(fmt.Sprintf("%T", testcase.event), func(t *testing.T) {
			a, err := MarshalEvent(testcase.event)
			if err != nil {
				t.Fatal(err)
			}
			if a.TypeUrl != testcase.url {
				t.Fatalf("unexpected url: %v != %v", a.TypeUrl, testcase.url)
			}

			var de DynamicEvent
			if err := UnmarshalEvent(a, &de); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(de.Event, testcase.event) {
				t.Fatalf("round trip failed %v != %v", de.Event, testcase.event)
			}
		})
	}
}

func BenchmarkMarshalEvent(b *testing.B) {
	ev := &events.TaskStart{}
	expected, err := MarshalEvent(ev)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		a, err := MarshalEvent(ev)
		if err != nil {
			b.Fatal(err)
		}
		if a.TypeUrl != expected.TypeUrl {
			b.Fatalf("incorrect type url: %v != %v", a, expected)
		}
	}
}
