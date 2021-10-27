package peer

import (
	"fmt"
	"time"

	pb "github.com/libp2p/go-libp2p-core/peer/pb"
	"github.com/libp2p/go-libp2p-core/record"

	ma "github.com/multiformats/go-multiaddr"

	"github.com/gogo/protobuf/proto"
)

var _ record.Record = (*PeerRecord)(nil)

func init() {
	record.RegisterType(&PeerRecord{})
}

// PeerRecordEnvelopeDomain is the domain string used for peer records contained in a Envelope.
const PeerRecordEnvelopeDomain = "libp2p-peer-record"

// PeerRecordEnvelopePayloadType is the type hint used to identify peer records in a Envelope.
// Defined in https://github.com/multiformats/multicodec/blob/master/table.csv
// with name "libp2p-peer-record".
var PeerRecordEnvelopePayloadType = []byte{0x03, 0x01}

// PeerRecord contains information that is broadly useful to share with other peers,
// either through a direct exchange (as in the libp2p identify protocol), or through
// a Peer Routing provider, such as a DHT.
//
// Currently, a PeerRecord contains the public listen addresses for a peer, but this
// is expected to expand to include other information in the future.
//
// PeerRecords are ordered in time by their Seq field. Newer PeerRecords must have
// greater Seq values than older records. The NewPeerRecord function will create
// a PeerRecord with a timestamp-based Seq value. The other PeerRecord fields should
// be set by the caller:
//
//    rec := peer.NewPeerRecord()
//    rec.PeerID = aPeerID
//    rec.Addrs = someAddrs
//
// Alternatively, you can construct a PeerRecord struct directly and use the TimestampSeq
// helper to set the Seq field:
//
//    rec := peer.PeerRecord{PeerID: aPeerID, Addrs: someAddrs, Seq: peer.TimestampSeq()}
//
// Failing to set the Seq field will not result in an error, however, a PeerRecord with a
// Seq value of zero may be ignored or rejected by other peers.
//
// PeerRecords are intended to be shared with other peers inside a signed
// routing.Envelope, and PeerRecord implements the routing.Record interface
// to facilitate this.
//
// To share a PeerRecord, first call Sign to wrap the record in a Envelope
// and sign it with the local peer's private key:
//
//     rec := &PeerRecord{PeerID: myPeerId, Addrs: myAddrs}
//     envelope, err := rec.Sign(myPrivateKey)
//
// The resulting record.Envelope can be marshalled to a []byte and shared
// publicly. As a convenience, the MarshalSigned method will produce the
// Envelope and marshal it to a []byte in one go:
//
//     rec := &PeerRecord{PeerID: myPeerId, Addrs: myAddrs}
//     recordBytes, err := rec.MarshalSigned(myPrivateKey)
//
// To validate and unmarshal a signed PeerRecord from a remote peer,
// "consume" the containing envelope, which will return both the
// routing.Envelope and the inner Record. The Record must be cast to
// a PeerRecord pointer before use:
//
//     envelope, untypedRecord, err := ConsumeEnvelope(envelopeBytes, PeerRecordEnvelopeDomain)
//     if err != nil {
//       handleError(err)
//       return
//     }
//     peerRec := untypedRecord.(*PeerRecord)
//
type PeerRecord struct {
	// PeerID is the ID of the peer this record pertains to.
	PeerID ID

	// Addrs contains the public addresses of the peer this record pertains to.
	Addrs []ma.Multiaddr

	// Seq is a monotonically-increasing sequence counter that's used to order
	// PeerRecords in time. The interval between Seq values is unspecified,
	// but newer PeerRecords MUST have a greater Seq value than older records
	// for the same peer.
	Seq uint64
}

// NewPeerRecord returns a PeerRecord with a timestamp-based sequence number.
// The returned record is otherwise empty and should be populated by the caller.
func NewPeerRecord() *PeerRecord {
	return &PeerRecord{Seq: TimestampSeq()}
}

// PeerRecordFromAddrInfo creates a PeerRecord from an AddrInfo struct.
// The returned record will have a timestamp-based sequence number.
func PeerRecordFromAddrInfo(info AddrInfo) *PeerRecord {
	rec := NewPeerRecord()
	rec.PeerID = info.ID
	rec.Addrs = info.Addrs
	return rec
}

// PeerRecordFromProtobuf creates a PeerRecord from a protobuf PeerRecord
// struct.
func PeerRecordFromProtobuf(msg *pb.PeerRecord) (*PeerRecord, error) {
	record := &PeerRecord{}

	var id ID
	if err := id.UnmarshalBinary(msg.PeerId); err != nil {
		return nil, err
	}

	record.PeerID = id
	record.Addrs = addrsFromProtobuf(msg.Addresses)
	record.Seq = msg.Seq

	return record, nil
}

// TimestampSeq is a helper to generate a timestamp-based sequence number for a PeerRecord.
func TimestampSeq() uint64 {
	return uint64(time.Now().UnixNano())
}

// Domain is used when signing and validating PeerRecords contained in Envelopes.
// It is constant for all PeerRecord instances.
func (r *PeerRecord) Domain() string {
	return PeerRecordEnvelopeDomain
}

// Codec is a binary identifier for the PeerRecord type. It is constant for all PeerRecord instances.
func (r *PeerRecord) Codec() []byte {
	return PeerRecordEnvelopePayloadType
}

// UnmarshalRecord parses a PeerRecord from a byte slice.
// This method is called automatically when consuming a record.Envelope
// whose PayloadType indicates that it contains a PeerRecord.
// It is generally not necessary or recommended to call this method directly.
func (r *PeerRecord) UnmarshalRecord(bytes []byte) error {
	if r == nil {
		return fmt.Errorf("cannot unmarshal PeerRecord to nil receiver")
	}

	var msg pb.PeerRecord
	err := proto.Unmarshal(bytes, &msg)
	if err != nil {
		return err
	}

	rPtr, err := PeerRecordFromProtobuf(&msg)
	if err != nil {
		return err
	}
	*r = *rPtr

	return nil
}

// MarshalRecord serializes a PeerRecord to a byte slice.
// This method is called automatically when constructing a routing.Envelope
// using Seal or PeerRecord.Sign.
func (r *PeerRecord) MarshalRecord() ([]byte, error) {
	msg, err := r.ToProtobuf()
	if err != nil {
		return nil, err
	}
	return proto.Marshal(msg)
}

// Equal returns true if the other PeerRecord is identical to this one.
func (r *PeerRecord) Equal(other *PeerRecord) bool {
	if other == nil {
		return r == nil
	}
	if r.PeerID != other.PeerID {
		return false
	}
	if r.Seq != other.Seq {
		return false
	}
	if len(r.Addrs) != len(other.Addrs) {
		return false
	}
	for i, _ := range r.Addrs {
		if !r.Addrs[i].Equal(other.Addrs[i]) {
			return false
		}
	}
	return true
}

// ToProtobuf returns the equivalent Protocol Buffer struct object of a PeerRecord.
func (r *PeerRecord) ToProtobuf() (*pb.PeerRecord, error) {
	idBytes, err := r.PeerID.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return &pb.PeerRecord{
		PeerId:    idBytes,
		Addresses: addrsToProtobuf(r.Addrs),
		Seq:       r.Seq,
	}, nil
}

func addrsFromProtobuf(addrs []*pb.PeerRecord_AddressInfo) []ma.Multiaddr {
	var out []ma.Multiaddr
	for _, addr := range addrs {
		a, err := ma.NewMultiaddrBytes(addr.Multiaddr)
		if err != nil {
			continue
		}
		out = append(out, a)
	}
	return out
}

func addrsToProtobuf(addrs []ma.Multiaddr) []*pb.PeerRecord_AddressInfo {
	var out []*pb.PeerRecord_AddressInfo
	for _, addr := range addrs {
		out = append(out, &pb.PeerRecord_AddressInfo{Multiaddr: addr.Bytes()})
	}
	return out
}
