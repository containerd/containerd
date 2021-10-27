package codec

import (
	"fmt"

	"github.com/polydawn/refmt/shared"
	"github.com/polydawn/refmt/tok"

	ipld "github.com/ipld/go-ipld-prime"
)

// Future work: we would like to remove the Marshal function,
// and in particular, stop seeing types from refmt (like shared.TokenSink) be visible.

// Marshal is a deprecated function.
// Please consider switching to one of the Encode functions of one of the subpackages instead.
//
// Marshal provides a very general node-to-tokens marshalling feature.
// It can handle either cbor or json by being combined with a refmt TokenSink.
//
// It is valid for all the data model types except links, which are only
// supported if the nodes are typed and provide additional information
// to clarify how links should be encoded through their type info.
// (The dag-cbor and dag-json formats can be used if links are of CID
// implementation and need to be encoded in a schemafree way.)
func Marshal(n ipld.Node, sink shared.TokenSink) error {
	var tk tok.Token
	return marshal(n, &tk, sink)
}

func marshal(n ipld.Node, tk *tok.Token, sink shared.TokenSink) error {
	switch n.Kind() {
	case ipld.Kind_Invalid:
		return fmt.Errorf("cannot traverse a node that is absent")
	case ipld.Kind_Null:
		tk.Type = tok.TNull
		_, err := sink.Step(tk)
		return err
	case ipld.Kind_Map:
		// Emit start of map.
		tk.Type = tok.TMapOpen
		tk.Length = int(n.Length()) // TODO: overflow check
		if _, err := sink.Step(tk); err != nil {
			return err
		}
		// Emit map contents (and recurse).
		for itr := n.MapIterator(); !itr.Done(); {
			k, v, err := itr.Next()
			if err != nil {
				return err
			}
			tk.Type = tok.TString
			tk.Str, err = k.AsString()
			if err != nil {
				return err
			}
			if _, err := sink.Step(tk); err != nil {
				return err
			}
			if err := marshal(v, tk, sink); err != nil {
				return err
			}
		}
		// Emit map close.
		tk.Type = tok.TMapClose
		_, err := sink.Step(tk)
		return err
	case ipld.Kind_List:
		// Emit start of list.
		tk.Type = tok.TArrOpen
		l := n.Length()
		tk.Length = int(l) // TODO: overflow check
		if _, err := sink.Step(tk); err != nil {
			return err
		}
		// Emit list contents (and recurse).
		for i := int64(0); i < l; i++ {
			v, err := n.LookupByIndex(i)
			if err != nil {
				return err
			}
			if err := marshal(v, tk, sink); err != nil {
				return err
			}
		}
		// Emit list close.
		tk.Type = tok.TArrClose
		_, err := sink.Step(tk)
		return err
	case ipld.Kind_Bool:
		v, err := n.AsBool()
		if err != nil {
			return err
		}
		tk.Type = tok.TBool
		tk.Bool = v
		_, err = sink.Step(tk)
		return err
	case ipld.Kind_Int:
		v, err := n.AsInt()
		if err != nil {
			return err
		}
		tk.Type = tok.TInt
		tk.Int = int64(v)
		_, err = sink.Step(tk)
		return err
	case ipld.Kind_Float:
		v, err := n.AsFloat()
		if err != nil {
			return err
		}
		tk.Type = tok.TFloat64
		tk.Float64 = v
		_, err = sink.Step(tk)
		return err
	case ipld.Kind_String:
		v, err := n.AsString()
		if err != nil {
			return err
		}
		tk.Type = tok.TString
		tk.Str = v
		_, err = sink.Step(tk)
		return err
	case ipld.Kind_Bytes:
		v, err := n.AsBytes()
		if err != nil {
			return err
		}
		tk.Type = tok.TBytes
		tk.Bytes = v
		_, err = sink.Step(tk)
		return err
	case ipld.Kind_Link:
		return fmt.Errorf("link emission not supported by this codec without a schema!  (maybe you want dag-cbor or dag-json)")
	default:
		panic("unreachable")
	}
}
