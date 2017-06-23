package events

import (
	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
)

const (
	typesPrefix = "types.containerd.io/"
)

// MarshalEvent marshal the event into an any, namespacing the type url to the
// containerd types.
func MarshalEvent(event Event) (*types.Any, error) {
	pb, ok := event.(proto.Message)
	if !ok {
		return nil, errors.Errorf("%T not a protobuf", event)
	}

	url := typesPrefix + proto.MessageName(pb)
	val, err := proto.Marshal(pb)
	if err != nil {
		return nil, err
	}

	return &types.Any{
		TypeUrl: url,
		Value:   val,
	}, nil
}

// DynamEvent acts as a holder type for unmarshaling events where the type is
// not previously known.
type DynamicEvent struct {
	Event
}

// UnmarshalEvent provides an event object based on the provided any.
//
// Use with DynamicEvent (or protobuf/types.DynamicAny) if the type is not
// known before hand.
func UnmarshalEvent(any *types.Any, event Event) error {
	switch v := event.(type) {
	case proto.Message:
		if err := types.UnmarshalAny(any, v); err != nil {
			return errors.Wrapf(err, "failed to unmarshal event %v", any)
		}
	case *DynamicEvent:
		var da types.DynamicAny

		if err := types.UnmarshalAny(any, &da); err != nil {
			return errors.Wrapf(err, "failed to unmarshal event %v", any)
		}
		v.Event = da.Message
	default:
		return errors.Errorf("unsupported event type: %T", event)
	}

	return nil

}

// Is returns true if the event in any will unmarashal into the provided event.
func Is(any *types.Any, event Event) bool {
	pb, ok := event.(proto.Message)
	if !ok {
		return false
	}

	return types.Is(any, pb)
}
