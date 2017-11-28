package exchange

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/typeurl"
	"github.com/pkg/errors"
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
	var wg sync.WaitGroup
	wg.Add(1)
	errChan := make(chan error)
	go func() {
		defer wg.Done()
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
	wg.Wait()
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

			if reflect.DeepEqual(received, testevents) {
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
			if err := exchange.Publish(ctx, testcase.input, event); errors.Cause(err) != testcase.err {
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
			if err := exchange.Forward(ctx, &envelope); errors.Cause(err) != testcase.err {
				if err == nil {
					t.Fatalf("expected error %v, received nil", testcase.err)
				} else {
					t.Fatalf("expected error %v, received %v", testcase.err, err)
				}
			}

		})
	}
}
