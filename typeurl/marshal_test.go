package typeurl_test

import (
	"fmt"
	"reflect"
	"testing"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/typeurl"
)

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
			a, err := typeurl.MarshalAny(testcase.event)
			if err != nil {
				t.Fatal(err)
			}
			if a.TypeUrl != testcase.url {
				t.Fatalf("unexpected url: %v != %v", a.TypeUrl, testcase.url)
			}

			v, err := typeurl.UnmarshalAny(a)
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
	expected, err := typeurl.MarshalAny(ev)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		a, err := typeurl.MarshalAny(ev)
		if err != nil {
			b.Fatal(err)
		}
		if a.TypeUrl != expected.TypeUrl {
			b.Fatalf("incorrect type url: %v != %v", a, expected)
		}
	}
}
