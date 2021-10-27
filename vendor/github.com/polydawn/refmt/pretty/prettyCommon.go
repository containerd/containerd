package pretty

var (
	decoOff       = []byte("\x1B[0m")
	decoType      = []byte("\x1B[1;34m")
	decoBrack     = []byte("\x1B[0;36m")
	decoTypeParam = []byte("\x1B[1;36m")
	decoTag       = []byte("\x1B[0;33m")
	decoTagParam  = []byte("\x1B[1;33m")
	decoValSigil  = []byte("\x1B[1;32m")
	decoValString = []byte("\x1B[0;32m")
)

var (
	wordTrue       = []byte("true")
	wordFalse      = []byte("false")
	wordNull       = []byte("null")
	wordArrOpenPt1 = bcat(decoType, []byte("Array"), decoBrack, []byte("<len:"), decoTypeParam)
	wordArrOpenPt2 = bcat(decoBrack, []byte("> ["), decoOff)
	wordArrClose   = bcat(decoBrack, []byte("]"), decoOff)
	wordMapOpenPt1 = bcat(decoType, []byte("Map"), decoBrack, []byte("<len:"), decoTypeParam)
	wordMapOpenPt2 = bcat(decoBrack, []byte("> {"), decoOff)
	wordMapClose   = bcat(decoBrack, []byte("}"), decoOff)
	wordColon      = bcat(decoBrack, []byte(": "), decoOff)
	wordTag        = bcat(decoTag, []byte("_tag:"), decoTagParam)
	wordTagClose   = bcat(decoTag, []byte("_ "), decoOff)
	wordUnknownLen = []byte("?")
	wordBreak      = []byte("\n\r")
)

func indentWord(depth int) (a []byte) {
	a = []byte{}
	for i := 0; i < depth; i++ {
		a = append(a, '\t')
	}
	return
}

func bcat(bss ...[]byte) []byte {
	l := 0
	for _, bs := range bss {
		l += len(bs)
	}
	rbs := make([]byte, 0, l)
	for _, bs := range bss {
		rbs = append(rbs, bs...)
	}
	return rbs
}
