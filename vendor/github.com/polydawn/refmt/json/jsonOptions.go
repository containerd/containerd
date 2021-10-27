package json

type EncodeOptions struct {
	// If set, this will be prefixed to every "line" to pretty-print.
	// (Typically you want to just set this to `[]byte{'\n'}`!)
	Line []byte

	// If set, this will be prefixed $N$ times before each line's content to pretty-print.
	// (Likely values are a tab, or a few spaces.)
	Indent []byte
}

// marker method -- you may use this type to instruct `refmt.Marshal`
// what kind of encoder to use.
func (EncodeOptions) IsEncodeOptions() {}

type DecodeOptions struct {
	// future: options to validate canonical serial order
}

// marker method -- you may use this type to instruct `refmt.Marshal`
// what kind of encoder to use.
func (DecodeOptions) IsDecodeOptions() {}
