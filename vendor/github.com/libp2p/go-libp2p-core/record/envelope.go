package record

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"github.com/libp2p/go-libp2p-core/crypto"
	pb "github.com/libp2p/go-libp2p-core/record/pb"

	pool "github.com/libp2p/go-buffer-pool"

	"github.com/gogo/protobuf/proto"
	"github.com/multiformats/go-varint"
)

// Envelope contains an arbitrary []byte payload, signed by a libp2p peer.
//
// Envelopes are signed in the context of a particular "domain", which is a
// string specified when creating and verifying the envelope. You must know the
// domain string used to produce the envelope in order to verify the signature
// and access the payload.
type Envelope struct {
	// The public key that can be used to verify the signature and derive the peer id of the signer.
	PublicKey crypto.PubKey

	// A binary identifier that indicates what kind of data is contained in the payload.
	// TODO(yusef): enforce multicodec prefix
	PayloadType []byte

	// The envelope payload.
	RawPayload []byte

	// The signature of the domain string :: type hint :: payload.
	signature []byte

	// the unmarshalled payload as a Record, cached on first access via the Record accessor method
	cached         Record
	unmarshalError error
	unmarshalOnce  sync.Once
}

var ErrEmptyDomain = errors.New("envelope domain must not be empty")
var ErrEmptyPayloadType = errors.New("payloadType must not be empty")
var ErrInvalidSignature = errors.New("invalid signature or incorrect domain")

// Seal marshals the given Record, places the marshaled bytes inside an Envelope,
// and signs with the given private key.
func Seal(rec Record, privateKey crypto.PrivKey) (*Envelope, error) {
	payload, err := rec.MarshalRecord()
	if err != nil {
		return nil, fmt.Errorf("error marshaling record: %v", err)
	}

	domain := rec.Domain()
	payloadType := rec.Codec()
	if domain == "" {
		return nil, ErrEmptyDomain
	}

	if len(payloadType) == 0 {
		return nil, ErrEmptyPayloadType
	}

	unsigned, err := makeUnsigned(domain, payloadType, payload)
	if err != nil {
		return nil, err
	}
	defer pool.Put(unsigned)

	sig, err := privateKey.Sign(unsigned)
	if err != nil {
		return nil, err
	}

	return &Envelope{
		PublicKey:   privateKey.GetPublic(),
		PayloadType: payloadType,
		RawPayload:  payload,
		signature:   sig,
	}, nil
}

// ConsumeEnvelope unmarshals a serialized Envelope and validates its
// signature using the provided 'domain' string. If validation fails, an error
// is returned, along with the unmarshalled envelope so it can be inspected.
//
// On success, ConsumeEnvelope returns the Envelope itself, as well as the inner payload,
// unmarshalled into a concrete Record type. The actual type of the returned Record depends
// on what has been registered for the Envelope's PayloadType (see RegisterType for details).
//
// You can type assert on the returned Record to convert it to an instance of the concrete
// Record type:
//
//    envelope, rec, err := ConsumeEnvelope(envelopeBytes, peer.PeerRecordEnvelopeDomain)
//    if err != nil {
//      handleError(envelope, err)  // envelope may be non-nil, even if errors occur!
//      return
//    }
//    peerRec, ok := rec.(*peer.PeerRecord)
//    if ok {
//      doSomethingWithPeerRecord(peerRec)
//    }
//
// Important: you MUST check the error value before using the returned Envelope. In some error
// cases, including when the envelope signature is invalid, both the Envelope and an error will
// be returned. This allows you to inspect the unmarshalled but invalid Envelope. As a result,
// you must not assume that any non-nil Envelope returned from this function is valid.
//
// If the Envelope signature is valid, but no Record type is registered for the Envelope's
// PayloadType, ErrPayloadTypeNotRegistered will be returned, along with the Envelope and
// a nil Record.
func ConsumeEnvelope(data []byte, domain string) (envelope *Envelope, rec Record, err error) {
	e, err := UnmarshalEnvelope(data)
	if err != nil {
		return nil, nil, fmt.Errorf("failed when unmarshalling the envelope: %w", err)
	}

	err = e.validate(domain)
	if err != nil {
		return e, nil, fmt.Errorf("failed to validate envelope: %w", err)
	}

	rec, err = e.Record()
	if err != nil {
		return e, nil, fmt.Errorf("failed to unmarshal envelope payload: %w", err)
	}
	return e, rec, nil
}

// ConsumeTypedEnvelope unmarshals a serialized Envelope and validates its
// signature. If validation fails, an error is returned, along with the unmarshalled
// envelope so it can be inspected.
//
// Unlike ConsumeEnvelope, ConsumeTypedEnvelope does not try to automatically determine
// the type of Record to unmarshal the Envelope's payload into. Instead, the caller provides
// a destination Record instance, which will unmarshal the Envelope payload. It is the caller's
// responsibility to determine whether the given Record type is able to unmarshal the payload
// correctly.
//
//    rec := &MyRecordType{}
//    envelope, err := ConsumeTypedEnvelope(envelopeBytes, rec)
//    if err != nil {
//      handleError(envelope, err)
//    }
//    doSomethingWithRecord(rec)
//
// Important: you MUST check the error value before using the returned Envelope. In some error
// cases, including when the envelope signature is invalid, both the Envelope and an error will
// be returned. This allows you to inspect the unmarshalled but invalid Envelope. As a result,
// you must not assume that any non-nil Envelope returned from this function is valid.
func ConsumeTypedEnvelope(data []byte, destRecord Record) (envelope *Envelope, err error) {
	e, err := UnmarshalEnvelope(data)
	if err != nil {
		return nil, fmt.Errorf("failed when unmarshalling the envelope: %w", err)
	}

	err = e.validate(destRecord.Domain())
	if err != nil {
		return e, fmt.Errorf("failed to validate envelope: %w", err)
	}

	err = destRecord.UnmarshalRecord(e.RawPayload)
	if err != nil {
		return e, fmt.Errorf("failed to unmarshal envelope payload: %w", err)
	}
	e.cached = destRecord
	return e, nil
}

// UnmarshalEnvelope unmarshals a serialized Envelope protobuf message,
// without validating its contents. Most users should use ConsumeEnvelope.
func UnmarshalEnvelope(data []byte) (*Envelope, error) {
	var e pb.Envelope
	if err := proto.Unmarshal(data, &e); err != nil {
		return nil, err
	}

	key, err := crypto.PublicKeyFromProto(e.PublicKey)
	if err != nil {
		return nil, err
	}

	return &Envelope{
		PublicKey:   key,
		PayloadType: e.PayloadType,
		RawPayload:  e.Payload,
		signature:   e.Signature,
	}, nil
}

// Marshal returns a byte slice containing a serialized protobuf representation
// of a Envelope.
func (e *Envelope) Marshal() ([]byte, error) {
	key, err := crypto.PublicKeyToProto(e.PublicKey)
	if err != nil {
		return nil, err
	}

	msg := pb.Envelope{
		PublicKey:   key,
		PayloadType: e.PayloadType,
		Payload:     e.RawPayload,
		Signature:   e.signature,
	}
	return proto.Marshal(&msg)
}

// Equal returns true if the other Envelope has the same public key,
// payload, payload type, and signature. This implies that they were also
// created with the same domain string.
func (e *Envelope) Equal(other *Envelope) bool {
	if other == nil {
		return e == nil
	}
	return e.PublicKey.Equals(other.PublicKey) &&
		bytes.Compare(e.PayloadType, other.PayloadType) == 0 &&
		bytes.Compare(e.signature, other.signature) == 0 &&
		bytes.Compare(e.RawPayload, other.RawPayload) == 0
}

// Record returns the Envelope's payload unmarshalled as a Record.
// The concrete type of the returned Record depends on which Record
// type was registered for the Envelope's PayloadType - see record.RegisterType.
//
// Once unmarshalled, the Record is cached for future access.
func (e *Envelope) Record() (Record, error) {
	e.unmarshalOnce.Do(func() {
		if e.cached != nil {
			return
		}
		e.cached, e.unmarshalError = unmarshalRecordPayload(e.PayloadType, e.RawPayload)
	})
	return e.cached, e.unmarshalError
}

// TypedRecord unmarshals the Envelope's payload to the given Record instance.
// It is the caller's responsibility to ensure that the Record type is capable
// of unmarshalling the Envelope payload. Callers can inspect the Envelope's
// PayloadType field to determine the correct type of Record to use.
//
// This method will always unmarshal the Envelope payload even if a cached record
// exists.
func (e *Envelope) TypedRecord(dest Record) error {
	return dest.UnmarshalRecord(e.RawPayload)
}

// validate returns nil if the envelope signature is valid for the given 'domain',
// or an error if signature validation fails.
func (e *Envelope) validate(domain string) error {
	unsigned, err := makeUnsigned(domain, e.PayloadType, e.RawPayload)
	if err != nil {
		return err
	}
	defer pool.Put(unsigned)

	valid, err := e.PublicKey.Verify(unsigned, e.signature)
	if err != nil {
		return fmt.Errorf("failed while verifying signature: %w", err)
	}
	if !valid {
		return ErrInvalidSignature
	}
	return nil
}

// makeUnsigned is a helper function that prepares a buffer to sign or verify.
// It returns a byte slice from a pool. The caller MUST return this slice to the
// pool.
func makeUnsigned(domain string, payloadType []byte, payload []byte) ([]byte, error) {
	var (
		fields = [][]byte{[]byte(domain), payloadType, payload}

		// fields are prefixed with their length as an unsigned varint. we
		// compute the lengths before allocating the sig buffer so we know how
		// much space to add for the lengths
		flen = make([][]byte, len(fields))
		size = 0
	)

	for i, f := range fields {
		l := len(f)
		flen[i] = varint.ToUvarint(uint64(l))
		size += l + len(flen[i])
	}

	b := pool.Get(size)

	var s int
	for i, f := range fields {
		s += copy(b[s:], flen[i])
		s += copy(b[s:], f)
	}

	return b[:s], nil
}
