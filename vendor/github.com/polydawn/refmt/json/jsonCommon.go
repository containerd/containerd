package json

//const (
//	jsonMajor
//)

// The most heavily used words, cached as byte slices.
var (
	wordTrue     = []byte("true")
	wordFalse    = []byte("false")
	wordNull     = []byte("null")
	wordArrOpen  = []byte("[")
	wordArrClose = []byte("]")
	wordMapOpen  = []byte("{")
	wordMapClose = []byte("}")
	wordColon    = []byte(":")
	wordComma    = []byte(",")
	wordSpace    = []byte(" ")
)
