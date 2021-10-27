package cbor

type EncodeOptions struct {
	// there aren't a ton of options for cbor, but we still need this
	// for use as a sigil for the top-level refmt methods to demux on.
}

// marker method -- you may use this type to instruct `refmt.Marshal`
// what kind of encoder to use.
func (EncodeOptions) IsEncodeOptions() {}

type DecodeOptions struct {
	CoerceUndefToNull bool

	// future: options to validate canonical serial order
}

// marker method -- you may use this type to instruct `refmt.Marshal`
// what kind of encoder to use.
func (DecodeOptions) IsDecodeOptions() {}
