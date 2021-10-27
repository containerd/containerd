package record

import (
	"errors"
	"reflect"
)

var (
	// ErrPayloadTypeNotRegistered is returned from ConsumeEnvelope when the Envelope's
	// PayloadType does not match any registered Record types.
	ErrPayloadTypeNotRegistered = errors.New("payload type is not registered")

	payloadTypeRegistry = make(map[string]reflect.Type)
)

// Record represents a data type that can be used as the payload of an Envelope.
// The Record interface defines the methods used to marshal and unmarshal a Record
// type to a byte slice.
//
// Record types may be "registered" as the default for a given Envelope.PayloadType
// using the RegisterType function. Once a Record type has been registered,
// an instance of that type will be created and used to unmarshal the payload of
// any Envelope with the registered PayloadType when the Envelope is opened using
// the ConsumeEnvelope function.
//
// To use an unregistered Record type instead, use ConsumeTypedEnvelope and pass in
// an instance of the Record type that you'd like the Envelope's payload to be
// unmarshaled into.
type Record interface {

	// Domain is the "signature domain" used when signing and verifying a particular
	// Record type. The Domain string should be unique to your Record type, and all
	// instances of the Record type must have the same Domain string.
	Domain() string

	// Codec is a binary identifier for this type of record, ideally a registered multicodec
	// (see https://github.com/multiformats/multicodec).
	// When a Record is put into an Envelope (see record.Seal), the Codec value will be used
	// as the Envelope's PayloadType. When the Envelope is later unsealed, the PayloadType
	// will be used to lookup the correct Record type to unmarshal the Envelope payload into.
	Codec() []byte

	// MarshalRecord converts a Record instance to a []byte, so that it can be used as an
	// Envelope payload.
	MarshalRecord() ([]byte, error)

	// UnmarshalRecord unmarshals a []byte payload into an instance of a particular Record type.
	UnmarshalRecord([]byte) error
}

// RegisterType associates a binary payload type identifier with a concrete
// Record type. This is used to automatically unmarshal Record payloads from Envelopes
// when using ConsumeEnvelope, and to automatically marshal Records and determine the
// correct PayloadType when calling Seal.
//
// Callers must provide an instance of the record type to be registered, which must be
// a pointer type. Registration should be done in the init function of the package
// where the Record type is defined:
//
//    package hello_record
//    import record "github.com/libp2p/go-libp2p-core/record"
//
//    func init() {
//        record.RegisterType(&HelloRecord{})
//    }
//
//    type HelloRecord struct { } // etc..
//
func RegisterType(prototype Record) {
	payloadTypeRegistry[string(prototype.Codec())] = getValueType(prototype)
}

func unmarshalRecordPayload(payloadType []byte, payloadBytes []byte) (Record, error) {
	rec, err := blankRecordForPayloadType(payloadType)
	if err != nil {
		return nil, err
	}
	err = rec.UnmarshalRecord(payloadBytes)
	if err != nil {
		return nil, err
	}
	return rec, nil
}

func blankRecordForPayloadType(payloadType []byte) (Record, error) {
	valueType, ok := payloadTypeRegistry[string(payloadType)]
	if !ok {
		return nil, ErrPayloadTypeNotRegistered
	}

	val := reflect.New(valueType)
	asRecord := val.Interface().(Record)
	return asRecord, nil
}

func getValueType(i interface{}) reflect.Type {
	valueType := reflect.TypeOf(i)
	if valueType.Kind() == reflect.Ptr {
		valueType = valueType.Elem()
	}
	return valueType
}
