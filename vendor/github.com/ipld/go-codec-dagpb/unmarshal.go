package dagpb

import (
	"fmt"
	"io"
	"io/ioutil"

	"github.com/ipfs/go-cid"
	ipld "github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"google.golang.org/protobuf/encoding/protowire"
)

// ErrIntOverflow is returned a varint overflows during decode, it indicates
// malformed data
var ErrIntOverflow = fmt.Errorf("protobuf: varint overflow")

// Decode provides an IPLD codec decode interface for DAG-PB data. Provide a
// compatible NodeAssembler and a byte source to unmarshal a DAG-PB IPLD Node.
// Use the NodeAssembler from the PBNode type for safest construction
// (Type.PBNode.NewBuilder()). A Map assembler will also work.
// This function is registered via the go-ipld-prime link loader for multicodec
// code 0x70 when this package is invoked via init.
func Decode(na ipld.NodeAssembler, in io.Reader) error {
	var src []byte
	if buf, ok := in.(interface{ Bytes() []byte }); ok {
		src = buf.Bytes()
	} else {
		var err error
		src, err = ioutil.ReadAll(in)
		if err != nil {
			return err
		}
	}
	return DecodeBytes(na, src)
}

// DecodeBytes is like Decode, but it uses an input buffer directly.
// Decode will grab or read all the bytes from an io.Reader anyway, so this can
// save having to copy the bytes or create a bytes.Buffer.
func DecodeBytes(na ipld.NodeAssembler, src []byte) error {
	remaining := src

	ma, err := na.BeginMap(2)
	if err != nil {
		return err
	}
	var links ipld.ListAssembler

	haveData := false
	haveLinks := false
	for {
		if len(remaining) == 0 {
			break
		}

		fieldNum, wireType, n := protowire.ConsumeTag(remaining)
		if n < 0 {
			return protowire.ParseError(n)
		}
		remaining = remaining[n:]

		if wireType != 2 {
			return fmt.Errorf("protobuf: (PBNode) invalid wireType, expected 2, got %d", wireType)
		}

		// Note that we allow Data and Links to come in either order,
		// since the spec defines that decoding "should" accept either form.
		// This is for backwards compatibility with older IPFS data.

		switch fieldNum {
		case 1:
			if haveData {
				return fmt.Errorf("protobuf: (PBNode) duplicate Data section")
			}

			chunk, n := protowire.ConsumeBytes(remaining)
			if n < 0 {
				return protowire.ParseError(n)
			}
			remaining = remaining[n:]

			if links != nil {
				// Links came before Data.
				// Finish them before we start Data.
				if err := links.Finish(); err != nil {
					return err
				}
				links = nil
			}

			if err := ma.AssembleKey().AssignString("Data"); err != nil {
				return err
			}
			if err := ma.AssembleValue().AssignBytes(chunk); err != nil {
				return err
			}
			haveData = true

		case 2:
			bytesLen, n := protowire.ConsumeVarint(remaining)
			if n < 0 {
				return protowire.ParseError(n)
			}
			remaining = remaining[n:]

			if links == nil {
				if haveLinks {
					return fmt.Errorf("protobuf: (PBNode) duplicate Links section")
				}

				// The repeated "Links" part begins.
				if err := ma.AssembleKey().AssignString("Links"); err != nil {
					return err
				}
				links, err = ma.AssembleValue().BeginList(0)
				if err != nil {
					return err
				}
			}

			curLink, err := links.AssembleValue().BeginMap(3)
			if err != nil {
				return err
			}
			if err := unmarshalLink(remaining[:bytesLen], curLink); err != nil {
				return err
			}
			remaining = remaining[bytesLen:]
			if err := curLink.Finish(); err != nil {
				return err
			}
			haveLinks = true

		default:
			return fmt.Errorf("protobuf: (PBNode) invalid fieldNumber, expected 1 or 2, got %d", fieldNum)
		}
	}

	if links != nil {
		// We had some links at the end, so finish them.
		if err := links.Finish(); err != nil {
			return err
		}

	} else if !haveLinks {
		// We didn't have any links.
		// Since we always want a Links field, add one here.
		if err := ma.AssembleKey().AssignString("Links"); err != nil {
			return err
		}
		links, err := ma.AssembleValue().BeginList(0)
		if err != nil {
			return err
		}
		if err := links.Finish(); err != nil {
			return err
		}
	}
	return ma.Finish()
}

func unmarshalLink(remaining []byte, ma ipld.MapAssembler) error {
	haveHash := false
	haveName := false
	haveTsize := false
	for {
		if len(remaining) == 0 {
			break
		}

		fieldNum, wireType, n := protowire.ConsumeTag(remaining)
		if n < 0 {
			return protowire.ParseError(n)
		}
		remaining = remaining[n:]

		switch fieldNum {
		case 1:
			if haveHash {
				return fmt.Errorf("protobuf: (PBLink) duplicate Hash section")
			}
			if haveName {
				return fmt.Errorf("protobuf: (PBLink) invalid order, found Name before Hash")
			}
			if haveTsize {
				return fmt.Errorf("protobuf: (PBLink) invalid order, found Tsize before Hash")
			}
			if wireType != 2 {
				return fmt.Errorf("protobuf: (PBLink) wrong wireType (%d) for Hash", wireType)
			}

			chunk, n := protowire.ConsumeBytes(remaining)
			if n < 0 {
				return protowire.ParseError(n)
			}
			remaining = remaining[n:]

			_, c, err := cid.CidFromBytes(chunk)
			if err != nil {
				return fmt.Errorf("invalid Hash field found in link, expected CID (%v)", err)
			}
			if err := ma.AssembleKey().AssignString("Hash"); err != nil {
				return err
			}
			if err := ma.AssembleValue().AssignLink(cidlink.Link{Cid: c}); err != nil {
				return err
			}
			haveHash = true

		case 2:
			if haveName {
				return fmt.Errorf("protobuf: (PBLink) duplicate Name section")
			}
			if haveTsize {
				return fmt.Errorf("protobuf: (PBLink) invalid order, found Tsize before Name")
			}
			if wireType != 2 {
				return fmt.Errorf("protobuf: (PBLink) wrong wireType (%d) for Name", wireType)
			}

			chunk, n := protowire.ConsumeBytes(remaining)
			if n < 0 {
				return protowire.ParseError(n)
			}
			remaining = remaining[n:]

			if err := ma.AssembleKey().AssignString("Name"); err != nil {
				return err
			}
			if err := ma.AssembleValue().AssignString(string(chunk)); err != nil {
				return err
			}
			haveName = true

		case 3:
			if haveTsize {
				return fmt.Errorf("protobuf: (PBLink) duplicate Tsize section")
			}
			if wireType != 0 {
				return fmt.Errorf("protobuf: (PBLink) wrong wireType (%d) for Tsize", wireType)
			}

			v, n := protowire.ConsumeVarint(remaining)
			if n < 0 {
				return protowire.ParseError(n)
			}
			remaining = remaining[n:]

			if err := ma.AssembleKey().AssignString("Tsize"); err != nil {
				return err
			}
			if err := ma.AssembleValue().AssignInt(int64(v)); err != nil {
				return err
			}
			haveTsize = true

		default:
			return fmt.Errorf("protobuf: (PBLink) invalid fieldNumber, expected 1, 2 or 3, got %d", fieldNum)
		}
	}

	if !haveHash {
		return fmt.Errorf("invalid Hash field found in link, expected CID")
	}

	return nil
}
