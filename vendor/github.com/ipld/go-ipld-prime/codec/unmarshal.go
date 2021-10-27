package codec

import (
	"fmt"
	"math"

	"github.com/polydawn/refmt/shared"
	"github.com/polydawn/refmt/tok"

	ipld "github.com/ipld/go-ipld-prime"
)

// Future work: we would like to remove the Unmarshal function,
// and in particular, stop seeing types from refmt (like shared.TokenSource) be visible.

// wishlist: if we could reconstruct the ipld.Path of an error while
//  *unwinding* from that error... that'd be nice.
//   (trying to build it proactively would waste tons of allocs on the happy path.)
//  we can do this; it just requires well-typed errors and a bunch of work.

// Tests for all this are in the ipld.Node impl tests!
//  They're effectively doing double duty: testing the builders, too.
//   (Is that sensible?  Should it be refactored?  Not sure; maybe!)

// Unmarshal is a deprecated function.
// Please consider switching to one of the Decode functions of one of the subpackages instead.
//
// Unmarshal provides a very general tokens-to-node unmarshalling feature.
// It can handle either cbor or json by being combined with a refmt TokenSink.
//
// The unmarshalled data is fed to the given NodeAssembler, which accumulates it;
// at the end, any error is returned from the Unmarshal method,
// and the user can pick up the finished Node from wherever their assembler has it.
// Typical usage might look like the following:
//
//		nb := basicnode.Prototype__Any{}.NewBuilder()
//		err := codec.Unmarshal(nb, json.Decoder(reader))
//		n := nb.Build()
//
// It is valid for all the data model types except links, which are only
// supported if the nodes are typed and provide additional information
// to clarify how links should be decoded through their type info.
// (The dag-cbor and dag-json formats can be used if links are of CID
// implementation and need to be decoded in a schemafree way.)
func Unmarshal(na ipld.NodeAssembler, tokSrc shared.TokenSource) error {
	var tk tok.Token
	done, err := tokSrc.Step(&tk)
	if err != nil {
		return err
	}
	if done && !tk.Type.IsValue() {
		return fmt.Errorf("unexpected eof")
	}
	return unmarshal(na, tokSrc, &tk)
}

// starts with the first token already primed.  Necessary to get recursion
//  to flow right without a peek+unpeek system.
func unmarshal(na ipld.NodeAssembler, tokSrc shared.TokenSource, tk *tok.Token) error {
	// FUTURE: check for schema.TypedNodeBuilder that's going to parse a Link (they can slurp any token kind they want).
	switch tk.Type {
	case tok.TMapOpen:
		expectLen := tk.Length
		allocLen := tk.Length
		if tk.Length == -1 {
			expectLen = math.MaxInt32
			allocLen = 0
		}
		ma, err := na.BeginMap(int64(allocLen))
		if err != nil {
			return err
		}
		observedLen := 0
		for {
			_, err := tokSrc.Step(tk)
			if err != nil {
				return err
			}
			switch tk.Type {
			case tok.TMapClose:
				if expectLen != math.MaxInt32 && observedLen != expectLen {
					return fmt.Errorf("unexpected mapClose before declared length")
				}
				return ma.Finish()
			case tok.TString:
				// continue
			default:
				return fmt.Errorf("unexpected %s token while expecting map key", tk.Type)
			}
			observedLen++
			if observedLen > expectLen {
				return fmt.Errorf("unexpected continuation of map elements beyond declared length")
			}
			mva, err := ma.AssembleEntry(tk.Str)
			if err != nil { // return in error if the key was rejected
				return err
			}
			err = Unmarshal(mva, tokSrc)
			if err != nil { // return in error if some part of the recursion errored
				return err
			}
		}
	case tok.TMapClose:
		return fmt.Errorf("unexpected mapClose token")
	case tok.TArrOpen:
		expectLen := tk.Length
		allocLen := tk.Length
		if tk.Length == -1 {
			expectLen = math.MaxInt32
			allocLen = 0
		}
		la, err := na.BeginList(int64(allocLen))
		if err != nil {
			return err
		}
		observedLen := 0
		for {
			_, err := tokSrc.Step(tk)
			if err != nil {
				return err
			}
			switch tk.Type {
			case tok.TArrClose:
				if expectLen != math.MaxInt32 && observedLen != expectLen {
					return fmt.Errorf("unexpected arrClose before declared length")
				}
				return la.Finish()
			default:
				observedLen++
				if observedLen > expectLen {
					return fmt.Errorf("unexpected continuation of array elements beyond declared length")
				}
				err := unmarshal(la.AssembleValue(), tokSrc, tk)
				if err != nil { // return in error if some part of the recursion errored
					return err
				}
			}
		}
	case tok.TArrClose:
		return fmt.Errorf("unexpected arrClose token")
	case tok.TNull:
		return na.AssignNull()
	case tok.TString:
		return na.AssignString(tk.Str)
	case tok.TBytes:
		return na.AssignBytes(tk.Bytes)
	case tok.TBool:
		return na.AssignBool(tk.Bool)
	case tok.TInt:
		return na.AssignInt(tk.Int)
	case tok.TUint:
		return na.AssignInt(int64(tk.Uint)) // FIXME overflow check
	case tok.TFloat64:
		return na.AssignFloat(tk.Float64)
	default:
		panic("unreachable")
	}
}
