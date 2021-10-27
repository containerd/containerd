// Package peer contains Protobuf and JSON serialization/deserialization methods for peer IDs.
package peer

import (
	"encoding"
	"encoding/json"
)

// Interface assertions commented out to avoid introducing hard dependencies to protobuf.
// var _ proto.Marshaler = (*ID)(nil)
// var _ proto.Unmarshaler = (*ID)(nil)
var _ json.Marshaler = (*ID)(nil)
var _ json.Unmarshaler = (*ID)(nil)

var _ encoding.BinaryMarshaler = (*ID)(nil)
var _ encoding.BinaryUnmarshaler = (*ID)(nil)
var _ encoding.TextMarshaler = (*ID)(nil)
var _ encoding.TextUnmarshaler = (*ID)(nil)

func (id ID) Marshal() ([]byte, error) {
	return []byte(id), nil
}

// MarshalBinary returns the byte representation of the peer ID.
func (id ID) MarshalBinary() ([]byte, error) {
	return id.Marshal()
}

func (id ID) MarshalTo(data []byte) (n int, err error) {
	return copy(data, []byte(id)), nil
}

func (id *ID) Unmarshal(data []byte) (err error) {
	*id, err = IDFromBytes(data)
	return err
}

// UnmarshalBinary sets the ID from its binary representation.
func (id *ID) UnmarshalBinary(data []byte) error {
	return id.Unmarshal(data)
}

// Size implements Gogo's proto.Sizer, but we omit the compile-time assertion to avoid introducing a hard
// dependency on gogo.
func (id ID) Size() int {
	return len([]byte(id))
}

func (id ID) MarshalJSON() ([]byte, error) {
	return json.Marshal(IDB58Encode(id))
}

func (id *ID) UnmarshalJSON(data []byte) (err error) {
	var v string
	if err = json.Unmarshal(data, &v); err != nil {
		return err
	}
	*id, err = IDB58Decode(v)
	return err
}

// MarshalText returns the text encoding of the ID.
func (id ID) MarshalText() ([]byte, error) {
	return []byte(IDB58Encode(id)), nil
}

// UnmarshalText restores the ID from its text encoding.
func (id *ID) UnmarshalText(data []byte) error {
	pid, err := IDB58Decode(string(data))
	if err != nil {
		return err
	}
	*id = pid
	return nil
}
