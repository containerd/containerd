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

package exchange

import (
	"context"
	"errors"
	"testing"
	"time"

	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/typeurl/v2"
	"github.com/google/go-cmp/cmp"
)

func TestExchangeBasic(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), t.Name())
	testevents := []events.Event{
		&eventstypes.ContainerCreate{ID: "asdf"},
		&eventstypes.ContainerCreate{ID: "qwer"},
		&eventstypes.ContainerCreate{ID: "zxcv"},
	}
	exchange := NewExchange()

	t.Log("subscribe")
	var cancel1, cancel2 func()

	// Create two subscribers for same set of events and make sure they
	// traverse the exchange.
	ctx1, cancel1 := context.WithCancel(ctx)
	eventq1, errq1 := exchange.Subscribe(ctx1)

	ctx2, cancel2 := context.WithCancel(ctx)
	eventq2, errq2 := exchange.Subscribe(ctx2)

	t.Log("publish")
	errChan := make(chan error)
	go func() {
		defer close(errChan)
		for _, event := range testevents {
			if err := exchange.Publish(ctx, "/test", event); err != nil {
				errChan <- err
				return
			}
		}

		t.Log("finished publishing")
	}()

	t.Log("waiting")
	if err := <-errChan; err != nil {
		t.Fatal(err)
	}

	for _, subscriber := range []struct {
		eventq <-chan *events.Envelope
		errq   <-chan error
		cancel func()
	}{
		{
			eventq: eventq1,
			errq:   errq1,
			cancel: cancel1,
		},
		{
			eventq: eventq2,
			errq:   errq2,
			cancel: cancel2,
		},
	} {
		var received []events.Event
	subscribercheck:
		for {
			select {
			case env := <-subscriber.eventq:
				ev, err := typeurl.UnmarshalAny(env.Event)
				if err != nil {
					t.Fatal(err)
				}
				received = append(received, ev.(*eventstypes.ContainerCreate))
			case err := <-subscriber.errq:
				if err != nil {
					t.Fatal(err)
				}
				break subscribercheck
			}

			if cmp.Equal(received, testevents, protobuf.Compare) {
				// when we do this, we expect the errs channel to be closed and
				// this will return.
				subscriber.cancel()
			}
		}
	}
}

func TestExchangeFilters(t *testing.T) {
	var (
		ctx      = namespaces.WithNamespace(context.Background(), t.Name())
		exchange = NewExchange()

		// config events, All events will be published
		containerCreateEvents = []events.Event{
			&eventstypes.ContainerCreate{ID: "asdf"},
			&eventstypes.ContainerCreate{ID: "qwer"},
			&eventstypes.ContainerCreate{ID: "zxcv"},
		}
		taskExitEvents = []events.Event{
			&eventstypes.TaskExit{ContainerID: "abcdef"},
		}
		testEventSets = []struct {
			topic  string
			events []events.Event
		}{
			{
				topic:  "/containers/create",
				events: containerCreateEvents,
			},
			{
				topic:  "/tasks/exit",
				events: taskExitEvents,
			},
		}
		allTestEvents = func(eventSets []struct {
			topic  string
			events []events.Event
		}) (events []events.Event) {
			for _, v := range eventSets {
				events = append(events, v.events...)
			}
			return
		}(testEventSets)

		// config test cases
		testCases = []struct {
			testName string
			filters  []string

			// The fields as below are for store data. Don't config them.
			expectEvents []events.Event
			eventq       <-chan *events.Envelope
			errq         <-chan error
			cancel       func()
		}{
			{
				testName:     "No Filter",
				expectEvents: allTestEvents,
			},
			{
				testName: "Filter events by one topic",
				filters: []string{
					`topic=="/containers/create"`,
				},
				expectEvents: containerCreateEvents,
			},
			{
				testName: "Filter events by field",
				filters: []string{
					"event.id",
				},
				expectEvents: containerCreateEvents,
			},
			{
				testName: "Filter events by field OR topic",
				filters: []string{
					`topic=="/containers/create"`,
					"event.id",
				},
				expectEvents: containerCreateEvents,
			},
			{
				testName: "Filter events by regex ",
				filters: []string{
					`topic~="/containers/*"`,
				},
				expectEvents: containerCreateEvents,
			},
			{
				testName: "Filter events for by anyone of two topics",
				filters: []string{
					`topic=="/tasks/exit"`,
					`topic=="/containers/create"`,
				},
				expectEvents: append(containerCreateEvents, taskExitEvents...),
			},
			{
				testName: "Filter events for by one topic AND id",
				filters: []string{
					`topic=="/containers/create",event.id=="qwer"`,
				},
				expectEvents: []events.Event{
					&eventstypes.ContainerCreate{ID: "qwer"},
				},
			},
		}
	)

	t.Log("subscribe")
	for i := range testCases {
		var ctx1 context.Context
		ctx1, testCases[i].cancel = context.WithCancel(ctx)
		testCases[i].eventq, testCases[i].errq = exchange.Subscribe(ctx1, testCases[i].filters...)
	}

	t.Log("publish")
	errChan := make(chan error)
	go func() {
		defer close(errChan)
		for _, es := range testEventSets {
			for _, e := range es.events {
				if err := exchange.Publish(ctx, es.topic, e); err != nil {
					errChan <- err
					return
				}
			}
		}

		t.Log("finished publishing")
	}()

	t.Log("waiting")
	if err := <-errChan; err != nil {
		t.Fatal(err)
	}

	t.Log("receive event")
	for _, subscriber := range testCases {
		t.Logf("test case: %q", subscriber.testName)
		var received []events.Event
	subscribercheck:
		for {
			select {
			case env := <-subscriber.eventq:
				ev, err := typeurl.UnmarshalAny(env.Event)
				if err != nil {
					t.Fatal(err)
				}
				received = append(received, ev)
			case err := <-subscriber.errq:
				if err != nil {
					t.Fatal(err)
				}
				break subscribercheck
			}

			if cmp.Equal(received, subscriber.expectEvents, protobuf.Compare) {
				// when we do this, we expect the errs channel to be closed and
				// this will return.
				subscriber.cancel()
			}
		}
	}
}

func TestExchangeValidateTopic(t *testing.T) {
	namespace := t.Name()
	ctx := namespaces.WithNamespace(context.Background(), namespace)
	exchange := NewExchange()

	for _, testcase := range []struct {
		input string
		err   error
	}{
		{
			input: "/test",
		},
		{
			input: "/test/test",
		},
		{
			input: "test",
			err:   errdefs.ErrInvalidArgument,
		},
	} {
		t.Run(testcase.input, func(t *testing.T) {
			event := &eventstypes.ContainerCreate{ID: t.Name()}
			if err := exchange.Publish(ctx, testcase.input, event); !errors.Is(err, testcase.err) {
				if err == nil {
					t.Fatalf("expected error %v, received nil", testcase.err)
				} else {
					t.Fatalf("expected error %v, received %v", testcase.err, err)
				}
			}

			evany, err := typeurl.MarshalAny(event)
			if err != nil {
				t.Fatal(err)
			}

			envelope := events.Envelope{
				Timestamp: time.Now().UTC(),
				Namespace: namespace,
				Topic:     testcase.input,
				Event:     evany,
			}

			// make sure we get same errors with forward.
			if err := exchange.Forward(ctx, &envelope); !errors.Is(err, testcase.err) {
				if err == nil {
					t.Fatalf("expected error %v, received nil", testcase.err)
				} else {
					t.Fatalf("expected error %v, received %v", testcase.err, err)
				}
			}

		})
	}
}
