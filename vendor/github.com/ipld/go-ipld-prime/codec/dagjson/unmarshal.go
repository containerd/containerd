package dagjson

import (
	"encoding/base64"
	"fmt"
	"io"

	cid "github.com/ipfs/go-cid"
	"github.com/polydawn/refmt/json"
	"github.com/polydawn/refmt/shared"
	"github.com/polydawn/refmt/tok"

	ipld "github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
)

// This drifts pretty far from the general unmarshal in the parent package:
//   - we know JSON never has length hints, so we ignore that field in tokens;
//   - we know JSON never has tags, so we ignore that field as well;
//   - we have dag-json's special sauce for detecting schemafree links
//      (and this unfortunately turns out to *significantly* convolute the first
//       several steps of handling maps, because it necessitates peeking several
//        tokens before deciding what kind of value to create).

// DecodeOptions can be used to customize the behavior of a decoding function.
// The Decode method on this struct fits the ipld.Decoder function interface.
type DecodeOptions struct {
	// If true, parse DAG-JSON `{"/":"cid string"}` as a Link kind node rather
	// than a plain map
	ParseLinks bool

	// If true, parse DAG-JSON `{"/":{"bytes":"base64 bytes..."}}` as a Bytes kind
	// node rather than nested plain maps
	ParseBytes bool
}

// Decode deserializes data from the given io.Reader and feeds it into the given ipld.NodeAssembler.
// Decode fits the ipld.Decoder function interface.
//
// The behavior of the decoder can be customized by setting fields in the DecodeOptions struct before calling this method.
func (cfg DecodeOptions) Decode(na ipld.NodeAssembler, r io.Reader) error {
	err := Unmarshal(na, json.NewDecoder(r), cfg)
	if err != nil {
		return err
	}
	// Slurp any remaining whitespace.
	//  This behavior may be due for review.
	//  (This is relevant if our reader is tee'ing bytes to a hasher, and
	//   the json contained any trailing whitespace.)
	//  (We can't actually support multiple objects per reader from here;
	//   we can't unpeek if we find a non-whitespace token, so our only
	//    option is to error if this reader seems to contain more content.)
	var buf [1]byte
	for {
		_, err := r.Read(buf[:])
		switch buf[0] {
		case ' ', 0x0, '\t', '\r', '\n': // continue
		default:
			return fmt.Errorf("unexpected content after end of json object")
		}
		if err == nil {
			continue
		} else if err == io.EOF {
			return nil
		} else {
			return err
		}
	}
}

// Future work: we would like to remove the Unmarshal function,
// and in particular, stop seeing types from refmt (like shared.TokenSource) be visible.
// Right now, some kinds of configuration (e.g. for whitespace and prettyprint) are only available through interacting with the refmt types;
// we should improve our API so that this can be done with only our own types in this package.

// Unmarshal is a deprecated function.
// Please consider switching to DecodeOptions.Decode instead.
func Unmarshal(na ipld.NodeAssembler, tokSrc shared.TokenSource, options DecodeOptions) error {
	var st unmarshalState
	st.options = options
	done, err := tokSrc.Step(&st.tk[0])
	if err != nil {
		return err
	}
	if done && !st.tk[0].Type.IsValue() && st.tk[0].Type != tok.TNull {
		return fmt.Errorf("unexpected eof")
	}
	return st.unmarshal(na, tokSrc)
}

type unmarshalState struct {
	tk      [7]tok.Token // mostly, only 0'th is used... but [1:7] are used during lookahead for links.
	shift   int          // how many times to slide something out of tk[1:7] instead of getting a new token.
	options DecodeOptions
}

// step leaves a "new" token in tk[0],
// taking account of an shift left by linkLookahead.
// It's only necessary to use this when handling maps,
// since the situations resulting in nonzero shift are otherwise unreachable.
//
// At most, 'step' will be shifting buffered tokens for:
//   - the first map key
//   - the first map value (which will be a string)
//   - the second map key
// and so (fortunately! whew!) we can do this in a fixed amount of memory,
// since none of those states can reach a recursion.
func (st *unmarshalState) step(tokSrc shared.TokenSource) error {
	switch st.shift {
	case 0:
		_, err := tokSrc.Step(&st.tk[0])
		return err
	case 1:
		st.tk[0] = st.tk[1]
		st.shift--
		return nil
	case 2:
		st.tk[0] = st.tk[1]
		st.tk[1] = st.tk[2]
		st.shift--
		return nil
	case 3:
		st.tk[0] = st.tk[1]
		st.tk[1] = st.tk[2]
		st.tk[2] = st.tk[3]
		st.shift--
		return nil
	case 4:
		st.tk[0] = st.tk[1]
		st.tk[1] = st.tk[2]
		st.tk[2] = st.tk[3]
		st.tk[3] = st.tk[4]
		st.shift--
		return nil
	case 5:
		st.tk[0] = st.tk[1]
		st.tk[1] = st.tk[2]
		st.tk[2] = st.tk[3]
		st.tk[3] = st.tk[4]
		st.tk[4] = st.tk[5]
		st.shift--
		return nil
	case 6:
		st.tk[0] = st.tk[1]
		st.tk[1] = st.tk[2]
		st.tk[2] = st.tk[3]
		st.tk[3] = st.tk[4]
		st.tk[4] = st.tk[5]
		st.tk[5] = st.tk[6]
		st.shift--
		return nil
	default:
		panic("unreachable")
	}
}

// ensure checks that the token lookahead-ahead (tk[lookhead]) is loaded from the underlying source.
func (st *unmarshalState) ensure(tokSrc shared.TokenSource, lookahead int) error {
	if st.shift < lookahead {
		if _, err := tokSrc.Step(&st.tk[lookahead]); err != nil {
			return err
		}
		st.shift = lookahead
	}
	return nil
}

// linkLookahead is called after receiving a TMapOpen token;
// when it returns, we will have either created a link, OR
// it's not a link, and the caller should proceed to start a map
// and while using st.step to ensure the peeked tokens are handled, OR
// in case of error, the error should just rise.
// If the bool return is true, we got a link, and you should not
// continue to attempt to build a map.
func (st *unmarshalState) linkLookahead(na ipld.NodeAssembler, tokSrc shared.TokenSource) (bool, error) {
	// Peek next token.  If it's a "/" string, link is still a possibility
	if err := st.ensure(tokSrc, 1); err != nil {
		return false, err
	}
	if st.tk[1].Type != tok.TString {
		return false, nil
	}
	if st.tk[1].Str != "/" {
		return false, nil
	}
	// Peek next token.  If it's a string, link is still a possibility.
	//  We won't try to parse it as a CID until we're sure it's the only thing in the map, though.
	if err := st.ensure(tokSrc, 2); err != nil {
		return false, err
	}
	if st.tk[2].Type != tok.TString {
		return false, nil
	}
	// Peek next token.  If it's map close, we've got a link!
	//  (Otherwise it had better be a string, because another map key is the
	//   only other valid transition here... but we'll leave that check to the caller.
	if err := st.ensure(tokSrc, 3); err != nil {
		return false, err
	}
	if st.tk[3].Type != tok.TMapClose {
		return false, nil
	}
	// Okay, we made it -- this looks like a link.  Parse it.
	//  If it *doesn't* parse as a CID, we treat this as an error.
	elCid, err := cid.Decode(st.tk[2].Str)
	if err != nil {
		return false, err
	}
	if err := na.AssignLink(cidlink.Link{Cid: elCid}); err != nil {
		return false, err
	}
	// consume the look-ahead tokens
	st.shift = 0
	return true, nil
}

func (st *unmarshalState) bytesLookahead(na ipld.NodeAssembler, tokSrc shared.TokenSource) (bool, error) {
	// Peek next token.  If it's a "/" string, bytes is still a possibility
	if err := st.ensure(tokSrc, 1); err != nil {
		return false, err
	}
	if st.tk[1].Type != tok.TString {
		return false, nil
	}
	if st.tk[1].Str != "/" {
		return false, nil
	}
	// Peek next token.  If it's a map, bytes is still a possibility.
	if err := st.ensure(tokSrc, 2); err != nil {
		return false, err
	}
	if st.tk[2].Type != tok.TMapOpen {
		return false, nil
	}
	// peek next token. If it's the string "bytes", we're on track.
	if err := st.ensure(tokSrc, 3); err != nil {
		return false, err
	}
	if st.tk[3].Type != tok.TString {
		return false, nil
	}
	if st.tk[3].Str != "bytes" {
		return false, nil
	}
	// peek next token. if it's a string, we're on track.
	if err := st.ensure(tokSrc, 4); err != nil {
		return false, err
	}
	if st.tk[4].Type != tok.TString {
		return false, nil
	}
	// peek next token. if it's the first map close we're on track.
	if err := st.ensure(tokSrc, 5); err != nil {
		return false, err
	}
	if st.tk[5].Type != tok.TMapClose {
		return false, nil
	}
	// Peek next token.  If it's map close, we've got bytes!
	if err := st.ensure(tokSrc, 6); err != nil {
		return false, err
	}
	if st.tk[6].Type != tok.TMapClose {
		return false, nil
	}
	// Okay, we made it -- this looks like bytes.  Parse it.
	elBytes, err := base64.RawStdEncoding.DecodeString(st.tk[4].Str)
	if err != nil {
		return false, err
	}
	if err := na.AssignBytes(elBytes); err != nil {
		return false, err
	}
	// consume the look-ahead tokens
	st.shift = 0
	return true, nil
}

// starts with the first token already primed.  Necessary to get recursion
//  to flow right without a peek+unpeek system.
func (st *unmarshalState) unmarshal(na ipld.NodeAssembler, tokSrc shared.TokenSource) error {
	// FUTURE: check for schema.TypedNodeBuilder that's going to parse a Link (they can slurp any token kind they want).
	switch st.tk[0].Type {
	case tok.TMapOpen:
		// dag-json has special needs: we pump a few tokens ahead to look for dag-json's "link" pattern.
		//  We can't actually call BeginMap until we're sure it's not gonna turn out to be a link.
		if st.options.ParseLinks {
			gotLink, err := st.linkLookahead(na, tokSrc)
			if err != nil { // return in error if any token peeks failed or if structure looked like a link but failed to parse as CID.
				return err
			}
			if gotLink {
				return nil
			}
		}

		if st.options.ParseBytes {
			gotBytes, err := st.bytesLookahead(na, tokSrc)
			if err != nil {
				return err
			}
			if gotBytes {
				return nil
			}
		}

		// Okay, now back to regularly scheduled map logic.
		ma, err := na.BeginMap(-1)
		if err != nil {
			return err
		}
		for {
			err := st.step(tokSrc) // shift next token into slot 0.
			if err != nil {        // return in error if next token unreadable
				return err
			}
			switch st.tk[0].Type {
			case tok.TMapClose:
				return ma.Finish()
			case tok.TString:
				// continue
			default:
				return fmt.Errorf("unexpected %s token while expecting map key", st.tk[0].Type)
			}
			mva, err := ma.AssembleEntry(st.tk[0].Str)
			if err != nil { // return in error if the key was rejected
				return err
			}
			// Do another shift so the next token is primed before we recurse.
			err = st.step(tokSrc)
			if err != nil { // return in error if next token unreadable
				return err
			}
			err = st.unmarshal(mva, tokSrc)
			if err != nil { // return in error if some part of the recursion errored
				return err
			}
		}
	case tok.TMapClose:
		return fmt.Errorf("unexpected mapClose token")
	case tok.TArrOpen:
		la, err := na.BeginList(-1)
		if err != nil {
			return err
		}
		for {
			_, err := tokSrc.Step(&st.tk[0])
			if err != nil {
				return err
			}
			switch st.tk[0].Type {
			case tok.TArrClose:
				return la.Finish()
			default:
				err := st.unmarshal(la.AssembleValue(), tokSrc)
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
		return na.AssignString(st.tk[0].Str)
	case tok.TBytes:
		return na.AssignBytes(st.tk[0].Bytes)
	case tok.TBool:
		return na.AssignBool(st.tk[0].Bool)
	case tok.TInt:
		return na.AssignInt(st.tk[0].Int)
	case tok.TUint:
		return na.AssignInt(int64(st.tk[0].Uint)) // FIXME overflow check
	case tok.TFloat64:
		return na.AssignFloat(st.tk[0].Float64)
	default:
		panic("unreachable")
	}
}
