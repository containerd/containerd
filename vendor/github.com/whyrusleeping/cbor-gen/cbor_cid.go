package typegen

import (
	"io"

	cid "github.com/ipfs/go-cid"
)

type CborCid cid.Cid

func (c *CborCid) MarshalCBOR(w io.Writer) error {
	return WriteCid(w, cid.Cid(*c))
}

func (c *CborCid) UnmarshalCBOR(r io.Reader) error {
	oc, err := ReadCid(r)
	if err != nil {
		return err
	}
	*c = CborCid(oc)
	return nil
}
